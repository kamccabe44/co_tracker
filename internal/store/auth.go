package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrBadCredentials is returned when a username/password pair does not match.
var ErrBadCredentials = errors.New("invalid username or password")

// Account is a login identity for the app itself — distinct from User,
// which is a person shown on the schedule.
type Account struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	IsAdmin            bool   `json:"isAdmin"`
	MustChangePassword bool   `json:"mustChangePassword"`
	PersonID           *int64 `json:"personId,omitempty"`
	CreatedAt          string `json:"createdAt"`
}

const accountColumns = `id, username, is_admin, must_change_password, person_id, created_at`

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Username, &a.IsAdmin, &a.MustChangePassword, &a.PersonID, &a.CreatedAt)
	return a, err
}

func hashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}

// SeedAccounts creates login accounts from a username -> password map when the
// accounts table is empty. The "admin" username gets admin privileges.
func (s *Store) SeedAccounts(users map[string]string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for username, password := range users {
		_, err := s.db.Exec(
			`INSERT INTO accounts (username, password_hash, is_admin) VALUES (?, ?, ?)`,
			username, hashPassword(password), username == "admin",
		)
		if err != nil {
			return fmt.Errorf("seed account %q: %w", username, err)
		}
	}
	return nil
}

// Authenticate returns the account matching the credentials, or ErrBadCredentials.
func (s *Store) Authenticate(username, password string) (Account, error) {
	a, err := scanAccount(s.db.QueryRow(
		`SELECT `+accountColumns+` FROM accounts
		 WHERE username = ? COLLATE NOCASE AND password_hash = ?`,
		strings.TrimSpace(username), hashPassword(password),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrBadCredentials
	}
	return a, err
}

func (s *Store) ListAccounts() ([]Account, error) {
	rows, err := s.db.Query(`SELECT ` + accountColumns + ` FROM accounts ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (s *Store) CreateAccount(username, password string, isAdmin bool) (Account, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Account{}, errors.New("username is required")
	}
	if len(password) < 4 {
		return Account{}, errors.New("password must be at least 4 characters")
	}
	res, err := s.db.Exec(
		`INSERT INTO accounts (username, password_hash, is_admin) VALUES (?, ?, ?)`,
		username, hashPassword(password), isAdmin,
	)
	if err != nil {
		if isUniqueErr(err) {
			return Account{}, fmt.Errorf("account %q: %w", username, ErrConflict)
		}
		return Account{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetAccount(id)
}

// AccountByPersonID returns the account linked to a roster person, if any.
func (s *Store) AccountByPersonID(personID int64) (Account, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE person_id = ?`, personID))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// UsernameExists reports whether an account with this username (case
// insensitive) already exists.
func (s *Store) UsernameExists(username string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE username = ? COLLATE NOCASE`, username).Scan(&n)
	return n > 0, err
}

// CreateAccountForPerson creates a login account linked to a roster person,
// with must_change_password set (the caller picks a known default
// password). The caller must first check AccountByPersonID — this always
// inserts, so calling it twice for the same person creates two accounts.
func (s *Store) CreateAccountForPerson(personID int64, username, password string) (Account, error) {
	username = strings.TrimSpace(username)
	res, err := s.db.Exec(
		`INSERT INTO accounts (username, password_hash, is_admin, must_change_password, person_id) VALUES (?, ?, 0, 1, ?)`,
		username, hashPassword(password), personID,
	)
	if err != nil {
		if isUniqueErr(err) {
			return Account{}, fmt.Errorf("account %q: %w", username, ErrConflict)
		}
		return Account{}, err
	}
	id, _ := res.LastInsertId()
	return s.GetAccount(id)
}

func (s *Store) GetAccount(id int64) (Account, error) {
	a, err := scanAccount(s.db.QueryRow(`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}

// CountAdmins returns how many accounts have admin privileges.
func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE is_admin`).Scan(&n)
	return n, err
}

func (s *Store) DeleteAccount(id int64) error {
	return s.deleteByID("accounts", id)
}

// ChangePassword updates an account's password after verifying the current
// one, and clears must_change_password — this is the one path that's allowed
// to clear it, since it proves the account holder chose the new password.
func (s *Store) ChangePassword(id int64, current, next string) error {
	res, err := s.db.Exec(
		`UPDATE accounts SET password_hash = ?, must_change_password = 0 WHERE id = ? AND password_hash = ?`,
		hashPassword(next), id, hashPassword(current),
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBadCredentials
	}
	return nil
}

// ResetPassword sets an account's password without checking the old one
// (admin action) and requires the account holder to change it again on next
// login — an admin-chosen password is, like a roster default, not one the
// account holder picked themselves.
func (s *Store) ResetPassword(id int64, password string) error {
	res, err := s.db.Exec(
		`UPDATE accounts SET password_hash = ?, must_change_password = 1 WHERE id = ?`,
		hashPassword(password), id,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Sessions ----

const sessionTimeFormat = "2006-01-02 15:04:05"

// CreateSession stores a new session for the account and returns its token.
func (s *Store) CreateSession(accountID int64, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().UTC().Add(ttl).Format(sessionTimeFormat)
	if _, err := s.db.Exec(
		`INSERT INTO sessions (token, account_id, expires_at) VALUES (?, ?, ?)`,
		token, accountID, expires,
	); err != nil {
		return "", err
	}
	// Opportunistically drop expired sessions so the table doesn't grow forever.
	now := time.Now().UTC().Format(sessionTimeFormat)
	s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now)
	return token, nil
}

// SessionAccount resolves a session token to its account, or ErrNotFound
// if the token is unknown or expired.
func (s *Store) SessionAccount(token string) (Account, error) {
	var a Account
	var expires string
	err := s.db.QueryRow(
		`SELECT a.id, a.username, a.is_admin, a.must_change_password, a.person_id, a.created_at, s.expires_at
		 FROM sessions s JOIN accounts a ON a.id = s.account_id
		 WHERE s.token = ?`, token,
	).Scan(&a.ID, &a.Username, &a.IsAdmin, &a.MustChangePassword, &a.PersonID, &a.CreatedAt, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	if expires < time.Now().UTC().Format(sessionTimeFormat) {
		s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}
