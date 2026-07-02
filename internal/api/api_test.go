package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kamccabe44/co_tracker/internal/store"
)

// newTestServer starts a server seeded with an admin account and returns it
// along with a client that is already logged in as admin.
func newTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SeedAccounts(map[string]string{"admin": "admin"}); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	static := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	ts := httptest.NewServer(New(st, static))
	t.Cleanup(ts.Close)
	return ts, login(t, ts, "admin", "admin")
}

// login returns a cookie-jar client holding a session for the credentials.
func login(t *testing.T, ts *httptest.Server, username, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	c := &http.Client{Jar: jar}
	res, err := c.PostForm(ts.URL+"/login", url.Values{
		"username": {username},
		"password": {password},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK { // after redirect to /
		t.Fatalf("login: status %d", res.StatusCode)
	}
	return c
}

func doJSON(t *testing.T, c *http.Client, method, url string, body any, want int) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("%s %s: got status %d, want %d", method, url, res.StatusCode, want)
	}
	if res.StatusCode == http.StatusNoContent {
		return nil
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func getList(t *testing.T, c *http.Client, url string) []map[string]any {
	t.Helper()
	res, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, res.StatusCode)
	}
	var out []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return out
}

func TestUnitAndEntryLifecycle(t *testing.T) {
	ts, c := newTestServer(t)

	unit := doJSON(t, c, "POST", ts.URL+"/api/units", map[string]string{"name": "Ops", "color": "#112233"}, http.StatusCreated)
	unitID := int64(unit["id"].(float64))

	// Duplicate unit name conflicts.
	doJSON(t, c, "POST", ts.URL+"/api/units", map[string]string{"name": "ops"}, http.StatusConflict)

	user := doJSON(t, c, "POST", ts.URL+"/api/users", map[string]string{"name": "Alice"}, http.StatusCreated)
	userID := int64(user["id"].(float64))

	entry := doJSON(t, c, "POST", ts.URL+"/api/entries", map[string]any{
		"date": "2026-07-06", "unitId": unitID, "userIds": []int64{userID}, "notes": "shift",
	}, http.StatusCreated)
	entryID := int64(entry["id"].(float64))

	entries := getList(t, c, ts.URL+"/api/entries?from=2026-07-01&to=2026-07-31")
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e["unitName"] != "Ops" || e["unitColor"] != "#112233" {
		t.Errorf("entry unit fields wrong: %v", e)
	}
	names := e["userNames"].([]any)
	if len(names) != 1 || names[0] != "Alice" {
		t.Errorf("userNames = %v, want [Alice]", names)
	}

	// Update: move date, clear users.
	doJSON(t, c, "PUT", fmt.Sprintf("%s/api/entries/%d", ts.URL, entryID), map[string]any{
		"date": "2026-07-07", "unitId": unitID, "userIds": []int64{},
	}, http.StatusOK)
	entries = getList(t, c, ts.URL+"/api/entries?from=2026-07-07&to=2026-07-07")
	if len(entries) != 1 || len(entries[0]["userNames"].([]any)) != 0 {
		t.Fatalf("updated entry wrong: %v", entries)
	}

	// Deleting the unit cascades to its entries.
	doJSON(t, c, "DELETE", fmt.Sprintf("%s/api/units/%d", ts.URL, unitID), nil, http.StatusNoContent)
	if entries := getList(t, c, ts.URL+"/api/entries?from=2026-07-01&to=2026-07-31"); len(entries) != 0 {
		t.Fatalf("entries not cascaded: %v", entries)
	}
}

func TestEntryValidation(t *testing.T) {
	ts, c := newTestServer(t)
	doJSON(t, c, "POST", ts.URL+"/api/entries", map[string]any{"date": "07/06/2026", "unitId": 1}, http.StatusBadRequest)
	doJSON(t, c, "POST", ts.URL+"/api/entries", map[string]any{"date": "2026-07-06"}, http.StatusBadRequest)
	doJSON(t, c, "PUT", ts.URL+"/api/entries/9999", map[string]any{"date": "2026-07-06", "unitId": 1}, http.StatusNotFound)
}

func postCSV(t *testing.T, c *http.Client, url, csvBody string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "data.csv")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	fw.Write([]byte(csvBody))
	mw.Close()
	res, err := c.Post(url, mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", url, res.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestImportUsersCSV(t *testing.T) {
	ts, c := newTestServer(t)
	out := postCSV(t, c, ts.URL+"/api/import/users", strings.Join([]string{
		"name,email",
		"Alice,alice@example.com",
		"Bob,",
		"alice,alice@new.example.com", // same name, case-insensitive: update
	}, "\n"))
	if out["created"].(float64) != 2 || out["updated"].(float64) != 1 {
		t.Fatalf("created/updated = %v/%v, want 2/1", out["created"], out["updated"])
	}
	users := getList(t, c, ts.URL+"/api/users")
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0]["email"] != "alice@new.example.com" {
		t.Errorf("email not updated: %v", users[0])
	}
}

func TestImportEntriesCSV(t *testing.T) {
	ts, c := newTestServer(t)
	out := postCSV(t, c, ts.URL+"/api/import/entries", strings.Join([]string{
		"date,unit,users,notes",
		"2026-07-06,Ops,Alice;Bob,Morning",
		"2026-07-07,Field Team,Alice,",
		"not-a-date,Ops,,",
	}, "\n"))
	if out["created"].(float64) != 2 {
		t.Fatalf("created = %v, want 2", out["created"])
	}
	if errs := out["errors"].([]any); len(errs) != 1 || !strings.Contains(errs[0].(string), "invalid date") {
		t.Fatalf("errors = %v, want one invalid-date error", out["errors"])
	}

	// Units and users were auto-created.
	if units := getList(t, c, ts.URL+"/api/units"); len(units) != 2 {
		t.Fatalf("got %d units, want 2", len(units))
	}
	if users := getList(t, c, ts.URL+"/api/users"); len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	entries := getList(t, c, ts.URL+"/api/entries?from=2026-07-06&to=2026-07-06")
	if len(entries) != 1 {
		t.Fatalf("got %d entries on 7/6, want 1", len(entries))
	}
	if names := entries[0]["userNames"].([]any); len(names) != 2 {
		t.Fatalf("userNames = %v, want 2 names", names)
	}
}

// ---- auth ----

func TestAuthRequired(t *testing.T) {
	ts, _ := newTestServer(t)

	// API without a session: 401 JSON.
	anon := &http.Client{}
	res, err := anon.Get(ts.URL + "/api/units")
	if err != nil {
		t.Fatalf("GET /api/units: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API: status %d, want 401", res.StatusCode)
	}

	// UI without a session: redirect to /login.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err = noRedirect.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(res.Header.Get("Location"), "/login") {
		t.Fatalf("unauthenticated UI: status %d location %q, want 302 to /login", res.StatusCode, res.Header.Get("Location"))
	}

	// Health check stays open.
	res, err = anon.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("healthz: status %d, want 200", res.StatusCode)
	}
}

func TestLoginLogout(t *testing.T) {
	ts, _ := newTestServer(t)

	// Wrong password is rejected.
	res, err := http.PostForm(ts.URL+"/login", url.Values{
		"username": {"admin"}, "password": {"wrong"},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad login: status %d, want 401", res.StatusCode)
	}

	// Correct credentials give a working session.
	c := login(t, ts, "admin", "admin")
	me := doJSON(t, c, "GET", ts.URL+"/api/auth/me", nil, http.StatusOK)
	if me["username"] != "admin" || me["isAdmin"] != true {
		t.Fatalf("me = %v, want admin/isAdmin", me)
	}

	// Logout invalidates the session.
	res, err = c.Get(ts.URL + "/logout")
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	res.Body.Close()
	res, err = c.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me after logout: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout: status %d, want 401", res.StatusCode)
	}
}

func TestAccountManagement(t *testing.T) {
	ts, admin := newTestServer(t)

	// Admin creates a regular account.
	acc := doJSON(t, admin, "POST", ts.URL+"/api/accounts",
		map[string]any{"username": "1stPlatoon", "password": "misfits", "isAdmin": false}, http.StatusCreated)
	accID := int64(acc["id"].(float64))

	// Duplicate username conflicts; short password rejected.
	doJSON(t, admin, "POST", ts.URL+"/api/accounts",
		map[string]any{"username": "1stplatoon", "password": "misfits"}, http.StatusConflict)
	doJSON(t, admin, "POST", ts.URL+"/api/accounts",
		map[string]any{"username": "x", "password": "abc"}, http.StatusBadRequest)

	// The new account can log in but is not an admin.
	member := login(t, ts, "1stPlatoon", "misfits")
	doJSON(t, member, "GET", ts.URL+"/api/accounts", nil, http.StatusForbidden)
	doJSON(t, member, "POST", ts.URL+"/api/accounts",
		map[string]any{"username": "sneaky", "password": "pass"}, http.StatusForbidden)

	// Admin resets the member's password; old session keeps working, new login uses it.
	doJSON(t, admin, "POST", fmt.Sprintf("%s/api/accounts/%d/reset-password", ts.URL, accID),
		map[string]any{"password": "silverbacks"}, http.StatusNoContent)
	login(t, ts, "1stPlatoon", "silverbacks")

	// Admin cannot delete itself (id 1 is the seeded admin) or the last admin.
	doJSON(t, admin, "DELETE", ts.URL+"/api/accounts/1", nil, http.StatusBadRequest)

	// Admin deletes the member account; their session dies with it.
	doJSON(t, admin, "DELETE", fmt.Sprintf("%s/api/accounts/%d", ts.URL, accID), nil, http.StatusNoContent)
	res, err := member.Get(ts.URL + "/api/auth/me")
	if err != nil {
		t.Fatalf("me after delete: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("deleted account session: status %d, want 401", res.StatusCode)
	}

	if accounts := getList(t, admin, ts.URL+"/api/accounts"); len(accounts) != 1 {
		t.Fatalf("got %d accounts, want 1", len(accounts))
	}
}

func TestChangePassword(t *testing.T) {
	ts, c := newTestServer(t)

	form := func(current, next, confirm string) int {
		t.Helper()
		res, err := c.PostForm(ts.URL+"/change-password", url.Values{
			"current_password": {current},
			"new_password":     {next},
			"confirm_password": {confirm},
		})
		if err != nil {
			t.Fatalf("change-password: %v", err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if got := form("admin", "newpass", "different"); got != http.StatusBadRequest {
		t.Fatalf("mismatched confirm: status %d, want 400", got)
	}
	if got := form("wrong", "newpass", "newpass"); got != http.StatusBadRequest {
		t.Fatalf("wrong current: status %d, want 400", got)
	}
	if got := form("admin", "newpass", "newpass"); got != http.StatusOK {
		t.Fatalf("change password: status %d, want 200", got)
	}

	// Old password no longer works; new one does.
	res, err := http.PostForm(ts.URL+"/login", url.Values{
		"username": {"admin"}, "password": {"admin"},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password login: status %d, want 401", res.StatusCode)
	}
	login(t, ts, "admin", "newpass")
}
