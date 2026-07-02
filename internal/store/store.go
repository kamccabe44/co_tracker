// Package store provides SQLite-backed persistence for co_tracker.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a uniqueness constraint would be violated.
var ErrConflict = errors.New("already exists")

type Store struct {
	db *sql.DB
}

type User struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`

	// Roster profile fields — all optional, filled in by a person or a roster
	// CSV import. Blank ("") means not yet known, not "cleared".
	Rank    string `json:"rank,omitempty"`
	Billet  string `json:"billet,omitempty"`
	Platoon string `json:"platoon,omitempty"`
	Sex     string `json:"sex,omitempty"`
	DOB     string `json:"dob,omitempty"`
	Vehicle string `json:"vehicle,omitempty"`
	Zone    string `json:"zone,omitempty"`
	Patrol  string `json:"patrol,omitempty"`
}

type Unit struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Entry is a schedule item: a business unit tracked on a given day,
// with zero or more people attached.
type Entry struct {
	ID        int64    `json:"id"`
	Date      string   `json:"date"` // YYYY-MM-DD
	UnitID    int64    `json:"unitId"`
	UnitName  string   `json:"unitName"`
	UnitColor string   `json:"unitColor"`
	Notes     string   `json:"notes,omitempty"`
	UserIDs   []int64  `json:"userIds"`
	UserNames []string `json:"userNames"`
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	name  TEXT NOT NULL,
	email TEXT NOT NULL DEFAULT '',
	UNIQUE(name COLLATE NOCASE)
);
CREATE TABLE IF NOT EXISTS units (
	id    INTEGER PRIMARY KEY AUTOINCREMENT,
	name  TEXT NOT NULL,
	color TEXT NOT NULL DEFAULT '#4f6df5',
	UNIQUE(name COLLATE NOCASE)
);
CREATE TABLE IF NOT EXISTS entries (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	date    TEXT NOT NULL,
	unit_id INTEGER NOT NULL REFERENCES units(id) ON DELETE CASCADE,
	notes   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_entries_date ON entries(date);
CREATE TABLE IF NOT EXISTS entry_users (
	entry_id INTEGER NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
	user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (entry_id, user_id)
);
CREATE TABLE IF NOT EXISTS accounts (
	id                   INTEGER PRIMARY KEY AUTOINCREMENT,
	username             TEXT NOT NULL,
	password_hash        TEXT NOT NULL,
	is_admin             INTEGER NOT NULL DEFAULT 0,
	must_change_password INTEGER NOT NULL DEFAULT 0,
	-- Set only for accounts provisioned from a roster import; NULL for
	-- accounts created directly (the seeded default admin, admin-added
	-- accounts). Lets a reupload recognize "this person already has an
	-- account" without depending on the generated username never colliding.
	person_id            INTEGER REFERENCES users(id) ON DELETE SET NULL,
	created_at           TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(username COLLATE NOCASE)
);
CREATE TABLE IF NOT EXISTS sessions (
	token      TEXT PRIMARY KEY,
	account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
	expires_at TEXT NOT NULL
);
`

// rosterColumns are added to a pre-existing users table via ALTER TABLE,
// since "CREATE TABLE IF NOT EXISTS" above is a no-op against a database
// that already has the table from before these columns existed.
var rosterColumns = []string{
	"rank", "billet", "platoon", "sex", "dob", "vehicle", "zone", "patrol",
}

// migrate adds columns introduced after a table's original CREATE TABLE, for
// databases that predate them. ALTER TABLE ADD COLUMN has no IF NOT EXISTS
// form in SQLite, so duplicate-column errors are expected and ignored.
func migrate(db *sql.DB) error {
	for _, col := range rosterColumns {
		_, err := db.Exec(fmt.Sprintf(`ALTER TABLE users ADD COLUMN %s TEXT NOT NULL DEFAULT ''`, col)) //nolint:gosec // col is a compile-time constant
		if err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("add users.%s: %w", col, err)
		}
	}
	if _, err := db.Exec(`ALTER TABLE accounts ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("add accounts.must_change_password: %w", err)
	}
	if _, err := db.Exec(`ALTER TABLE accounts ADD COLUMN person_id INTEGER REFERENCES users(id) ON DELETE SET NULL`); err != nil && !isDuplicateColumnErr(err) {
		return fmt.Errorf("add accounts.person_id: %w", err)
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}

// Open opens (creating if needed) the SQLite database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite serializes writes; a single connection avoids
	// SQLITE_BUSY under concurrent request handling.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const userColumns = `id, name, email, rank, billet, platoon, sex, dob, vehicle, zone, patrol`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.Rank, &u.Billet, &u.Platoon,
		&u.Sex, &u.DOB, &u.Vehicle, &u.Zone, &u.Patrol)
	return u, err
}

// ---- Users ----

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(name, email string) (User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, errors.New("name is required")
	}
	res, err := s.db.Exec(`INSERT INTO users (name, email) VALUES (?, ?)`, name, strings.TrimSpace(email))
	if err != nil {
		if isUniqueErr(err) {
			return User{}, fmt.Errorf("user %q: %w", name, ErrConflict)
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Name: name, Email: email}, nil
}

// UpsertUser inserts a user or updates the email of an existing user with the
// same name (case-insensitive). Returns the user ID and whether it was created.
func (s *Store) UpsertUser(name, email string) (int64, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, false, errors.New("name is required")
	}
	email = strings.TrimSpace(email)
	var id int64
	err := s.db.QueryRow(`SELECT id FROM users WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	switch {
	case err == nil:
		if email != "" {
			if _, err := s.db.Exec(`UPDATE users SET email = ? WHERE id = ?`, email, id); err != nil {
				return 0, false, err
			}
		}
		return id, false, nil
	case errors.Is(err, sql.ErrNoRows):
		res, err := s.db.Exec(`INSERT INTO users (name, email) VALUES (?, ?)`, name, email)
		if err != nil {
			return 0, false, err
		}
		id, _ := res.LastInsertId()
		return id, true, nil
	default:
		return 0, false, err
	}
}

func (s *Store) DeleteUser(id int64) error {
	return s.deleteByID("users", id)
}

// ---- Units ----

func (s *Store) ListUnits() ([]Unit, error) {
	rows, err := s.db.Query(`SELECT id, name, color FROM units ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	units := []Unit{}
	for rows.Next() {
		var u Unit
		if err := rows.Scan(&u.ID, &u.Name, &u.Color); err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	return units, rows.Err()
}

func (s *Store) CreateUnit(name, color string) (Unit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Unit{}, errors.New("name is required")
	}
	if color == "" {
		color = s.nextColor()
	}
	res, err := s.db.Exec(`INSERT INTO units (name, color) VALUES (?, ?)`, name, color)
	if err != nil {
		if isUniqueErr(err) {
			return Unit{}, fmt.Errorf("unit %q: %w", name, ErrConflict)
		}
		return Unit{}, err
	}
	id, _ := res.LastInsertId()
	return Unit{ID: id, Name: name, Color: color}, nil
}

func (s *Store) UpdateUnit(id int64, name, color string) (Unit, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Unit{}, errors.New("name is required")
	}
	res, err := s.db.Exec(`UPDATE units SET name = ?, color = ? WHERE id = ?`, name, color, id)
	if err != nil {
		if isUniqueErr(err) {
			return Unit{}, fmt.Errorf("unit %q: %w", name, ErrConflict)
		}
		return Unit{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Unit{}, ErrNotFound
	}
	return Unit{ID: id, Name: name, Color: color}, nil
}

func (s *Store) DeleteUnit(id int64) error {
	return s.deleteByID("units", id)
}

// GetOrCreateUnitByName returns the unit with the given name, creating it
// with a deterministic default color if it does not exist.
func (s *Store) GetOrCreateUnitByName(name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("unit name is required")
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM units WHERE name = ? COLLATE NOCASE`, name).Scan(&id)
	switch {
	case err == nil:
		return id, nil
	case errors.Is(err, sql.ErrNoRows):
		u, err := s.CreateUnit(name, "")
		if err != nil {
			return 0, err
		}
		return u.ID, nil
	default:
		return 0, err
	}
}

// ---- Entries ----

// ListEntries returns entries with date in [from, to] inclusive
// (both YYYY-MM-DD), with unit info and attached user names resolved.
func (s *Store) ListEntries(from, to string) ([]Entry, error) {
	rows, err := s.db.Query(`
		SELECT e.id, e.date, e.notes, u.id, u.name, u.color
		FROM entries e JOIN units u ON u.id = e.unit_id
		WHERE e.date >= ? AND e.date <= ?
		ORDER BY e.date, u.name COLLATE NOCASE, e.id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := []Entry{}
	index := map[int64]int{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Date, &e.Notes, &e.UnitID, &e.UnitName, &e.UnitColor); err != nil {
			return nil, err
		}
		e.UserIDs = []int64{}
		e.UserNames = []string{}
		index[e.ID] = len(entries)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	urows, err := s.db.Query(`
		SELECT eu.entry_id, us.id, us.name
		FROM entry_users eu
		JOIN users us ON us.id = eu.user_id
		JOIN entries e ON e.id = eu.entry_id
		WHERE e.date >= ? AND e.date <= ?
		ORDER BY us.name COLLATE NOCASE`, from, to)
	if err != nil {
		return nil, err
	}
	defer urows.Close()
	for urows.Next() {
		var entryID, userID int64
		var name string
		if err := urows.Scan(&entryID, &userID, &name); err != nil {
			return nil, err
		}
		if i, ok := index[entryID]; ok {
			entries[i].UserIDs = append(entries[i].UserIDs, userID)
			entries[i].UserNames = append(entries[i].UserNames, name)
		}
	}
	return entries, urows.Err()
}

// SaveEntry creates (id == 0) or replaces (id != 0) an entry and its user links.
func (s *Store) SaveEntry(id int64, date string, unitID int64, notes string, userIDs []int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if id == 0 {
		res, err := tx.Exec(`INSERT INTO entries (date, unit_id, notes) VALUES (?, ?, ?)`, date, unitID, notes)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	} else {
		res, err := tx.Exec(`UPDATE entries SET date = ?, unit_id = ?, notes = ? WHERE id = ?`, date, unitID, notes, id)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return 0, ErrNotFound
		}
		if _, err := tx.Exec(`DELETE FROM entry_users WHERE entry_id = ?`, id); err != nil {
			return 0, err
		}
	}
	for _, uid := range userIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO entry_users (entry_id, user_id) VALUES (?, ?)`, id, uid); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) DeleteEntry(id int64) error {
	return s.deleteByID("entries", id)
}

func (s *Store) deleteByID(table string, id int64) error {
	res, err := s.db.Exec(`DELETE FROM `+table+` WHERE id = ?`, id) //nolint:gosec // table is a compile-time constant
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// palette used for units created without an explicit color (e.g. CSV import).
var palette = []string{
	"#4f6df5", "#12a594", "#e5484d", "#ffb224", "#8e4ec6",
	"#0091ff", "#46a758", "#f76b15", "#d6409f", "#00a2c7",
}

// nextColor cycles through the palette by unit count, so units created
// without an explicit color (e.g. via CSV import) get distinct colors.
func (s *Store) nextColor() string {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM units`).Scan(&n); err != nil {
		n = 0
	}
	return palette[n%len(palette)]
}
