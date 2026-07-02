package api

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/kamccabe44/co_tracker/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

var pageTemplates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

const (
	sessionCookie = "co_tracker_session"
	sessionTTL    = 14 * 24 * time.Hour // matches os_alerts' cookie lifetime
)

type ctxKey int

const accountKey ctxKey = 0

// currentAccount returns the authenticated account stored by requireAuth.
func currentAccount(r *http.Request) store.Account {
	a, _ := r.Context().Value(accountKey).(store.Account)
	return a
}

// openPath reports whether the path is reachable without authentication.
// /vendor holds the embedded Bootstrap assets the login page needs.
func openPath(path string) bool {
	switch path {
	case "/login", "/logout", "/healthz":
		return true
	}
	return strings.HasPrefix(path, "/vendor/")
}

// passwordChangeExemptPaths stay reachable for an account with
// MustChangePassword set — everything else force-redirects to /change-password
// until they pick a new one.
var passwordChangeExemptPaths = map[string]bool{
	"/change-password": true,
	"/logout":           true,
	"/api/auth/me":      true, // the frontend needs this to know to redirect
}

// requireAuth redirects unauthenticated browser requests to /login and
// rejects unauthenticated API requests with 401. Once authenticated, an
// account with MustChangePassword set (roster-provisioned or admin-reset)
// is confined to the change-password flow until it picks its own password.
func (s *server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if openPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			if account, err := s.st.SessionAccount(cookie.Value); err == nil {
				if account.MustChangePassword && !passwordChangeExemptPaths[r.URL.Path] {
					if strings.HasPrefix(r.URL.Path, "/api/") {
						writeJSON(w, http.StatusForbidden, map[string]string{
							"error":              "password change required",
							"mustChangePassword": "true",
						})
						return
					}
					http.Redirect(w, r, "/change-password", http.StatusFound)
					return
				}
				ctx := context.WithValue(r.Context(), accountKey, account)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusFound)
	})
}

// requireAdmin wraps an API handler so only admin accounts can call it.
func requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !currentAccount(r).IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
			return
		}
		h(w, r)
	}
}

// safeNext keeps the post-login redirect on this site.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}

func renderPage(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := pageTemplates.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// ---- login / logout ----

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if _, err := s.st.SessionAccount(cookie.Value); err == nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
	}
	renderPage(w, http.StatusOK, "login.html", map[string]string{
		"Next": safeNext(r.URL.Query().Get("next")),
	})
}

func (s *server) doLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form data")
		return
	}
	next := safeNext(r.PostFormValue("next"))
	account, err := s.st.Authenticate(r.PostFormValue("username"), r.PostFormValue("password"))
	if err != nil {
		if errors.Is(err, store.ErrBadCredentials) {
			renderPage(w, http.StatusUnauthorized, "login.html", map[string]string{
				"Next":  next,
				"Error": "Invalid username or password.",
			})
			return
		}
		writeErr(w, err)
		return
	}
	token, err := s.st.CreateSession(account.ID, sessionTTL)
	if err != nil {
		writeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.st.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ---- change password ----

func (s *server) changePasswordPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, http.StatusOK, "change_password.html", map[string]any{
		"Required": currentAccount(r).MustChangePassword,
	})
}

func (s *server) doChangePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		badRequest(w, "invalid form data")
		return
	}
	wasRequired := currentAccount(r).MustChangePassword
	render := func(errMsg string, success bool) {
		status := http.StatusOK
		if errMsg != "" {
			status = http.StatusBadRequest
		}
		renderPage(w, status, "change_password.html", map[string]any{
			"Error":    errMsg,
			"Success":  success,
			"Required": wasRequired && !success,
		})
	}

	current := r.PostFormValue("current_password")
	next := r.PostFormValue("new_password")
	if next != r.PostFormValue("confirm_password") {
		render("New passwords do not match.", false)
		return
	}
	if len(next) < 4 {
		render("New password must be at least 4 characters.", false)
		return
	}
	err := s.st.ChangePassword(currentAccount(r).ID, current, next)
	if errors.Is(err, store.ErrBadCredentials) {
		render("Current password is incorrect.", false)
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	render("", true)
}

// ---- account management API (mirrors os_alerts' user management) ----

func (s *server) authMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentAccount(r))
}

func (s *server) listAccounts(w http.ResponseWriter, _ *http.Request) {
	accounts, err := s.st.ListAccounts()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *server) createAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Username) == "" {
		badRequest(w, "username is required")
		return
	}
	if len(in.Password) < 4 {
		badRequest(w, "password must be at least 4 characters")
		return
	}
	account, err := s.st.CreateAccount(in.Username, in.Password, in.IsAdmin)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == currentAccount(r).ID {
		badRequest(w, "cannot delete your own account")
		return
	}
	target, err := s.st.GetAccount(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if target.IsAdmin {
		admins, err := s.st.CountAdmins()
		if err != nil {
			writeErr(w, err)
			return
		}
		if admins <= 1 {
			badRequest(w, "cannot delete the last admin")
			return
		}
	}
	if err := s.st.DeleteAccount(id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) resetAccountPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < 4 {
		badRequest(w, "password must be at least 4 characters")
		return
	}
	if err := s.st.ResetPassword(id, in.Password); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
