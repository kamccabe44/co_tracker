package api

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxCSVBytes = 10 << 20 // 10 MiB

// importUsersCSV accepts a multipart upload (field "file") or a raw text/csv
// body with columns: name,email (email optional). A header row is detected
// and skipped. Existing users (matched by name) get their email updated.
func (s *server) importUsersCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(w, r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	created, updated := 0, 0
	var errs []string
	for i, rec := range rows {
		if i == 0 && isHeader(rec, "name") {
			continue
		}
		name := field(rec, 0)
		if name == "" {
			continue
		}
		_, isNew, err := s.st.UpsertUser(name, field(rec, 1))
		if err != nil {
			errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		if isNew {
			created++
		} else {
			updated++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": created,
		"updated": updated,
		"errors":  errs,
	})
}

// importEntriesCSV accepts CSV with columns: date,unit,users,notes.
// - date:  YYYY-MM-DD
// - unit:  business unit name (created with a default color if unknown)
// - users: semicolon-separated names (created if unknown); may be empty
// - notes: optional free text
func (s *server) importEntriesCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(w, r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	created := 0
	var errs []string
	for i, rec := range rows {
		if i == 0 && isHeader(rec, "date") {
			continue
		}
		date := field(rec, 0)
		if date == "" {
			continue
		}
		if !validDate(date) {
			errs = append(errs, fmt.Sprintf("row %d: invalid date %q (want YYYY-MM-DD)", i+1, date))
			continue
		}
		unitName := field(rec, 1)
		if unitName == "" {
			errs = append(errs, fmt.Sprintf("row %d: missing unit name", i+1))
			continue
		}
		unitID, err := s.st.GetOrCreateUnitByName(unitName)
		if err != nil {
			errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		var userIDs []int64
		rowOK := true
		for _, name := range strings.Split(field(rec, 2), ";") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			uid, _, err := s.st.UpsertUser(name, "")
			if err != nil {
				errs = append(errs, fmt.Sprintf("row %d: user %q: %v", i+1, name, err))
				rowOK = false
				break
			}
			userIDs = append(userIDs, uid)
		}
		if !rowOK {
			continue
		}
		if _, err := s.st.SaveEntry(0, date, unitID, field(rec, 3), userIDs); err != nil {
			errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
			continue
		}
		created++
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": created,
		"errors":  errs,
	})
}

func readCSV(w http.ResponseWriter, r *http.Request) ([][]string, error) {
	var src io.Reader
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxCSVBytes); err != nil {
			return nil, fmt.Errorf("parse upload: %w", err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf(`missing "file" form field`)
		}
		defer f.Close()
		src = f
	} else {
		src = http.MaxBytesReader(w, r.Body, maxCSVBytes)
	}
	cr := csv.NewReader(src)
	cr.FieldsPerRecord = -1 // allow ragged rows; missing columns read as ""
	cr.TrimLeadingSpace = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("CSV is empty")
	}
	return rows, nil
}

func field(rec []string, i int) string {
	if i >= len(rec) {
		return ""
	}
	return strings.TrimSpace(rec[i])
}

func isHeader(rec []string, firstCol string) bool {
	return len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), firstCol)
}
