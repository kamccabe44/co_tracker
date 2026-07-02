package store

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
)

// UpsertRosterPerson creates or updates a person by name (first + last,
// matched case-insensitively against the existing name column) and applies
// the roster profile fields. Blank field values never overwrite existing
// data — a CSV that doesn't yet know someone's vehicle shouldn't erase a
// vehicle assigned earlier through the app or a previous, fuller import.
func (s *Store) UpsertRosterPerson(first, last string, fields RosterFields) (id int64, isNew bool, err error) {
	first, last = strings.TrimSpace(first), strings.TrimSpace(last)
	name := strings.TrimSpace(first + " " + last)
	if name == "" {
		return 0, false, errors.New("first and last name are required")
	}

	err = s.db.QueryRow(`SELECT id FROM users WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	switch {
	case err == nil:
		_, execErr := s.db.Exec(`
			UPDATE users SET
				rank    = COALESCE(NULLIF(?, ''), rank),
				billet  = COALESCE(NULLIF(?, ''), billet),
				platoon = COALESCE(NULLIF(?, ''), platoon),
				sex     = COALESCE(NULLIF(?, ''), sex),
				dob     = COALESCE(NULLIF(?, ''), dob),
				vehicle = COALESCE(NULLIF(?, ''), vehicle),
				zone    = COALESCE(NULLIF(?, ''), zone),
				patrol  = COALESCE(NULLIF(?, ''), patrol)
			WHERE id = ?`,
			fields.Rank, fields.Billet, fields.Platoon, fields.Sex,
			fields.DOB, fields.Vehicle, fields.Zone, fields.Patrol, id)
		return id, false, execErr
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}

	res, err := s.db.Exec(`
		INSERT INTO users (name, rank, billet, platoon, sex, dob, vehicle, zone, patrol)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, fields.Rank, fields.Billet, fields.Platoon, fields.Sex,
		fields.DOB, fields.Vehicle, fields.Zone, fields.Patrol)
	if err != nil {
		return 0, false, err
	}
	id, _ = res.LastInsertId()
	return id, true, nil
}

// RosterFields are the optional profile columns a roster CSV can supply.
type RosterFields struct {
	Rank, Billet, Platoon, Sex, DOB, Vehicle, Zone, Patrol string
}

var usernameSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

// GenerateUsername builds a login username from a person's name:
// firstname.lastname, lowercased, non-alphanumeric characters stripped.
// Collisions are resolved by the caller via a numeric suffix, since only it
// can check uniqueness against the accounts table.
func GenerateUsername(first, last string) string {
	f := usernameSanitizer.ReplaceAllString(strings.ToLower(strings.TrimSpace(first)), "")
	l := usernameSanitizer.ReplaceAllString(strings.ToLower(strings.TrimSpace(last)), "")
	switch {
	case f != "" && l != "":
		return f + "." + l
	case l != "":
		return l
	default:
		return f
	}
}
