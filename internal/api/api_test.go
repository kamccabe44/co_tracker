package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kamccabe44/co_tracker/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	static := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	ts := httptest.NewServer(New(st, static))
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url string, body any, want int) map[string]any {
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
	res, err := http.DefaultClient.Do(req)
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

func getList(t *testing.T, url string) []map[string]any {
	t.Helper()
	res, err := http.Get(url)
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
	ts := newTestServer(t)

	unit := doJSON(t, "POST", ts.URL+"/api/units", map[string]string{"name": "Ops", "color": "#112233"}, http.StatusCreated)
	unitID := int64(unit["id"].(float64))

	// Duplicate unit name conflicts.
	doJSON(t, "POST", ts.URL+"/api/units", map[string]string{"name": "ops"}, http.StatusConflict)

	user := doJSON(t, "POST", ts.URL+"/api/users", map[string]string{"name": "Alice"}, http.StatusCreated)
	userID := int64(user["id"].(float64))

	entry := doJSON(t, "POST", ts.URL+"/api/entries", map[string]any{
		"date": "2026-07-06", "unitId": unitID, "userIds": []int64{userID}, "notes": "shift",
	}, http.StatusCreated)
	entryID := int64(entry["id"].(float64))

	entries := getList(t, ts.URL+"/api/entries?from=2026-07-01&to=2026-07-31")
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
	doJSON(t, "PUT", fmt.Sprintf("%s/api/entries/%d", ts.URL, entryID), map[string]any{
		"date": "2026-07-07", "unitId": unitID, "userIds": []int64{},
	}, http.StatusOK)
	entries = getList(t, ts.URL+"/api/entries?from=2026-07-07&to=2026-07-07")
	if len(entries) != 1 || len(entries[0]["userNames"].([]any)) != 0 {
		t.Fatalf("updated entry wrong: %v", entries)
	}

	// Deleting the unit cascades to its entries.
	doJSON(t, "DELETE", fmt.Sprintf("%s/api/units/%d", ts.URL, unitID), nil, http.StatusNoContent)
	if entries := getList(t, ts.URL+"/api/entries?from=2026-07-01&to=2026-07-31"); len(entries) != 0 {
		t.Fatalf("entries not cascaded: %v", entries)
	}
}

func TestEntryValidation(t *testing.T) {
	ts := newTestServer(t)
	doJSON(t, "POST", ts.URL+"/api/entries", map[string]any{"date": "07/06/2026", "unitId": 1}, http.StatusBadRequest)
	doJSON(t, "POST", ts.URL+"/api/entries", map[string]any{"date": "2026-07-06"}, http.StatusBadRequest)
	doJSON(t, "PUT", ts.URL+"/api/entries/9999", map[string]any{"date": "2026-07-06", "unitId": 1}, http.StatusNotFound)
}

func postCSV(t *testing.T, url, csvBody string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "data.csv")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	fw.Write([]byte(csvBody))
	mw.Close()
	res, err := http.Post(url, mw.FormDataContentType(), &buf)
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
	ts := newTestServer(t)
	out := postCSV(t, ts.URL+"/api/import/users", strings.Join([]string{
		"name,email",
		"Alice,alice@example.com",
		"Bob,",
		"alice,alice@new.example.com", // same name, case-insensitive: update
	}, "\n"))
	if out["created"].(float64) != 2 || out["updated"].(float64) != 1 {
		t.Fatalf("created/updated = %v/%v, want 2/1", out["created"], out["updated"])
	}
	users := getList(t, ts.URL+"/api/users")
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0]["email"] != "alice@new.example.com" {
		t.Errorf("email not updated: %v", users[0])
	}
}

func TestImportEntriesCSV(t *testing.T) {
	ts := newTestServer(t)
	out := postCSV(t, ts.URL+"/api/import/entries", strings.Join([]string{
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
	if units := getList(t, ts.URL+"/api/units"); len(units) != 2 {
		t.Fatalf("got %d units, want 2", len(units))
	}
	if users := getList(t, ts.URL+"/api/users"); len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	entries := getList(t, ts.URL+"/api/entries?from=2026-07-06&to=2026-07-06")
	if len(entries) != 1 {
		t.Fatalf("got %d entries on 7/6, want 1", len(entries))
	}
	if names := entries[0]["userNames"].([]any); len(names) != 2 {
		t.Fatalf("userNames = %v, want 2 names", names)
	}
}
