package api

import (
	"fmt"
	"net/http"

	"github.com/kamccabe44/co_tracker/internal/store"
)

// importRosterCSV accepts a roster export with columns:
//
//	Billet,Patrol,Rank,Last,First,PLT,Sex,DOB,Vehicle,Zone
//
// Only Last and First are required; every other column may be blank (a
// blank cell never overwrites data already on file — it just means "not
// provided this time"). Re-uploading the same roster later to fill in
// previously-blank columns (DOB, Vehicle, Zone, Patrol, ...) is the normal
// way to use this: it updates existing people in place rather than
// duplicating them.
//
// Each new person also gets a login account seeded with the password
// "password" and must-change-password set, so re-running an import never
// touches the credentials of someone who has already signed in.
func (s *server) importRosterCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := readCSV(w, r)
	if err != nil {
		badRequest(w, err.Error())
		return
	}

	var peopleCreated, peopleUpdated, accountsCreated int
	var errs []string

	for i, rec := range rows {
		if i == 0 && isHeader(rec, "billet") {
			continue
		}
		last := field(rec, 3)
		first := field(rec, 4)
		if last == "" && first == "" {
			continue
		}
		if last == "" || first == "" {
			errs = append(errs, rowErr(i, "both First and Last name are required"))
			continue
		}

		personID, isNew, err := s.st.UpsertRosterPerson(first, last, store.RosterFields{
			Billet:  field(rec, 0),
			Patrol:  field(rec, 1),
			Rank:    field(rec, 2),
			Platoon: field(rec, 5),
			Sex:     field(rec, 6),
			DOB:     field(rec, 7),
			Vehicle: field(rec, 8),
			Zone:    field(rec, 9),
		})
		if err != nil {
			errs = append(errs, rowErr(i, err.Error()))
			continue
		}
		if isNew {
			peopleCreated++
		} else {
			peopleUpdated++
		}

		if _, err := s.st.AccountByPersonID(personID); err == nil {
			continue // already has a login — never touch its credentials on reupload
		}
		username, err := s.uniqueUsername(first, last)
		if err != nil {
			errs = append(errs, rowErr(i, "generating username: "+err.Error()))
			continue
		}
		if _, err := s.st.CreateAccountForPerson(personID, username, defaultRosterPassword); err != nil {
			errs = append(errs, rowErr(i, "creating login: "+err.Error()))
			continue
		}
		accountsCreated++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"peopleCreated":   peopleCreated,
		"peopleUpdated":   peopleUpdated,
		"accountsCreated": accountsCreated,
		"errors":          errs,
	})
}

// defaultRosterPassword is the shared initial password for roster-provisioned
// accounts. must_change_password forces it to be replaced on first login.
const defaultRosterPassword = "password"

// uniqueUsername generates a login username for (first, last) and appends
// a numeric suffix if it's already taken by an unrelated account.
func (s *server) uniqueUsername(first, last string) (string, error) {
	base := store.GenerateUsername(first, last)
	username := base
	for n := 2; ; n++ {
		taken, err := s.st.UsernameExists(username)
		if err != nil {
			return "", err
		}
		if !taken {
			return username, nil
		}
		username = fmt.Sprintf("%s%d", base, n)
	}
}

func rowErr(i int, msg string) string {
	return fmt.Sprintf("row %d: %s", i+1, msg)
}
