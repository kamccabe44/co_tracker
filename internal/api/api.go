// Package api wires HTTP handlers for the co_tracker REST API and static UI.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/kamccabe44/co_tracker/internal/store"
)

type server struct {
	st *store.Store
}

// New returns the application handler: /api/* routes plus the embedded UI.
func New(st *store.Store, staticFS fs.FS) http.Handler {
	s := &server{st: st}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.doLogin)
	mux.HandleFunc("GET /logout", s.logout)
	mux.HandleFunc("GET /change-password", s.changePasswordPage)
	mux.HandleFunc("POST /change-password", s.doChangePassword)

	mux.HandleFunc("GET /api/auth/me", s.authMe)
	mux.HandleFunc("GET /api/accounts", requireAdmin(s.listAccounts))
	mux.HandleFunc("POST /api/accounts", requireAdmin(s.createAccount))
	mux.HandleFunc("DELETE /api/accounts/{id}", requireAdmin(s.deleteAccount))
	mux.HandleFunc("POST /api/accounts/{id}/reset-password", requireAdmin(s.resetAccountPassword))

	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("POST /api/users", s.createUser)
	mux.HandleFunc("DELETE /api/users/{id}", s.deleteUser)
	mux.HandleFunc("POST /api/import/users", s.importUsersCSV)
	mux.HandleFunc("POST /api/import/roster", requireAdmin(s.importRosterCSV))

	mux.HandleFunc("GET /api/units", s.listUnits)
	mux.HandleFunc("POST /api/units", s.createUnit)
	mux.HandleFunc("PUT /api/units/{id}", s.updateUnit)
	mux.HandleFunc("DELETE /api/units/{id}", s.deleteUnit)

	mux.HandleFunc("GET /api/entries", s.listEntries)
	mux.HandleFunc("POST /api/entries", s.saveEntry)
	mux.HandleFunc("PUT /api/entries/{id}", s.saveEntry)
	mux.HandleFunc("DELETE /api/entries/{id}", s.deleteEntry)
	mux.HandleFunc("POST /api/import/entries", s.importEntriesCSV)

	mux.Handle("/", http.FileServerFS(staticFS))
	return logRequests(s.requireAuth(mux))
}

// ---- Users ----

func (s *server) listUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.st.ListUsers()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.st.CreateUser(in.Name, in.Email)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	s.deleteByID(w, r, s.st.DeleteUser)
}

// ---- Units ----

func (s *server) listUnits(w http.ResponseWriter, _ *http.Request) {
	units, err := s.st.ListUnits()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, units)
}

func (s *server) createUnit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.st.CreateUnit(in.Name, in.Color)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *server) updateUnit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decode(w, r, &in) {
		return
	}
	u, err := s.st.UpdateUnit(id, in.Name, in.Color)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *server) deleteUnit(w http.ResponseWriter, r *http.Request) {
	s.deleteByID(w, r, s.st.DeleteUnit)
}

// ---- Entries ----

func (s *server) listEntries(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) || !validDate(to) {
		badRequest(w, "from and to query params are required as YYYY-MM-DD")
		return
	}
	entries, err := s.st.ListEntries(from, to)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *server) saveEntry(w http.ResponseWriter, r *http.Request) {
	var id int64
	if r.Method == http.MethodPut {
		var ok bool
		if id, ok = pathID(w, r); !ok {
			return
		}
	}
	var in struct {
		Date    string  `json:"date"`
		UnitID  int64   `json:"unitId"`
		Notes   string  `json:"notes"`
		UserIDs []int64 `json:"userIds"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validDate(in.Date) {
		badRequest(w, "date must be YYYY-MM-DD")
		return
	}
	if in.UnitID == 0 {
		badRequest(w, "unitId is required")
		return
	}
	savedID, err := s.st.SaveEntry(id, in.Date, in.UnitID, in.Notes, in.UserIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]int64{"id": savedID})
}

func (s *server) deleteEntry(w http.ResponseWriter, r *http.Request) {
	s.deleteByID(w, r, s.st.DeleteEntry)
}

// ---- helpers ----

func (s *server) deleteByID(w http.ResponseWriter, r *http.Request, del func(int64) error) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := del(id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		badRequest(w, "invalid id")
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		badRequest(w, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		log.Printf("internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
