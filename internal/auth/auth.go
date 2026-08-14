// Package auth handles password hashing, session lifecycle, and request
// authentication for the reviz-accounting app.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName      = "reviz_session"
	SessionDuration = 30 * 24 * time.Hour
	BcryptCost      = 12
)

// Roles in increasing privilege.
const (
	RoleViewer     = "viewer"
	RoleAccountant = "accountant"
	RoleOwner      = "owner"
)

var roleRank = map[string]int{
	RoleViewer:     1,
	RoleAccountant: 2,
	RoleOwner:      3,
}

// User is the minimal user record needed by the rest of the app.
type User struct {
	ID          int64
	Username    string
	FullName    string
	Role        string
	Active      bool
	CreatedAt   string
	LastLoginAt sql.NullString
}

// AtLeast reports whether the user's role meets the given minimum.
func (u *User) AtLeast(role string) bool {
	if u == nil {
		return false
	}
	return roleRank[u.Role] >= roleRank[role]
}

// ----- password hashing -----

func HashPassword(plain string) (string, error) {
	if len(plain) < 6 {
		return "", errors.New("密碼長度需 ≥ 6")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(plain), BcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// ----- user CRUD -----

func CreateUser(d *sql.DB, username, password, role string) (*User, error) {
	return CreateUserWithName(d, username, password, role, "")
}

func CreateUserWithName(d *sql.DB, username, password, role, fullName string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("帳號不可為空")
	}
	if _, ok := roleRank[role]; !ok {
		return nil, fmt.Errorf("不支援的角色: %s", role)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	var id int64
	err = d.QueryRow(
		`INSERT INTO users(username, full_name, password_hash, role) VALUES(?,?,?,?) RETURNING id`,
		username, strings.TrimSpace(fullName), hash, role,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return GetUser(d, id)
}

func GetUser(d *sql.DB, id int64) (*User, error) {
	u := &User{}
	var active int
	err := d.QueryRow(
		`SELECT id, username, full_name, role, active, created_at, last_login_at
		 FROM users WHERE id=?`, id,
	).Scan(&u.ID, &u.Username, &u.FullName, &u.Role, &active, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, err
	}
	u.Active = active == 1
	return u, nil
}

func GetUserByUsername(d *sql.DB, username string) (*User, string, error) {
	u := &User{}
	var active int
	var hash string
	err := d.QueryRow(
		`SELECT id, username, full_name, password_hash, role, active, created_at, last_login_at
		 FROM users WHERE username=?`, username,
	).Scan(&u.ID, &u.Username, &u.FullName, &hash, &u.Role, &active, &u.CreatedAt, &u.LastLoginAt)
	if err != nil {
		return nil, "", err
	}
	u.Active = active == 1
	return u, hash, nil
}

func ListUsers(d *sql.DB) ([]User, error) {
	rows, err := d.Query(
		`SELECT id, username, full_name, role, active, created_at, last_login_at
		 FROM users ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var active int
		if err := rows.Scan(&u.ID, &u.Username, &u.FullName, &u.Role, &active, &u.CreatedAt, &u.LastLoginAt); err != nil {
			return nil, err
		}
		u.Active = active == 1
		out = append(out, u)
	}
	return out, rows.Err()
}

func UpdateUserFullName(d *sql.DB, id int64, fullName string) error {
	_, err := d.Exec(`UPDATE users SET full_name=? WHERE id=?`, strings.TrimSpace(fullName), id)
	return err
}

func CountUsers(d *sql.DB) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func CountActiveOwners(d *sql.DB) (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE role='owner' AND active=1`).Scan(&n)
	return n, err
}

func UpdateUserRole(d *sql.DB, id int64, role string) error {
	if _, ok := roleRank[role]; !ok {
		return fmt.Errorf("不支援的角色: %s", role)
	}
	_, err := d.Exec(`UPDATE users SET role=? WHERE id=?`, role, id)
	return err
}

func SetUserActive(d *sql.DB, id int64, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := d.Exec(`UPDATE users SET active=? WHERE id=?`, v, id)
	return err
}

func UpdateUserPassword(d *sql.DB, id int64, newPassword string) error {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, id)
	return err
}

func DeleteUser(d *sql.DB, id int64) error {
	_, err := d.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

// ----- session lifecycle -----

func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func CreateSession(d *sql.DB, userID int64, userAgent, ip string) (string, error) {
	sid, err := newSessionID()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(SessionDuration).UTC().Format(time.RFC3339)
	_, err = d.Exec(
		`INSERT INTO sessions(id, user_id, expires_at, user_agent, ip) VALUES(?,?,?,?,?)`,
		sid, userID, expires, userAgent, ip,
	)
	if err != nil {
		return "", err
	}
	_, _ = d.Exec(`UPDATE users SET last_login_at=CURRENT_TIMESTAMP::text WHERE id=?`, userID)
	return sid, nil
}

func userBySession(d *sql.DB, sid string) (*User, error) {
	if sid == "" {
		return nil, sql.ErrNoRows
	}
	u := &User{}
	var active int
	var expires string
	err := d.QueryRow(`
		SELECT u.id, u.username, u.full_name, u.role, u.active, u.created_at, u.last_login_at, s.expires_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id=?`, sid,
	).Scan(&u.ID, &u.Username, &u.FullName, &u.Role, &active, &u.CreatedAt, &u.LastLoginAt, &expires)
	if err != nil {
		return nil, err
	}
	u.Active = active == 1
	exp, err := time.Parse(time.RFC3339, expires)
	if err == nil && time.Now().After(exp) {
		_, _ = d.Exec(`DELETE FROM sessions WHERE id=?`, sid)
		return nil, sql.ErrNoRows
	}
	if !u.Active {
		return nil, sql.ErrNoRows
	}
	return u, nil
}

func DeleteSession(d *sql.DB, sid string) error {
	if sid == "" {
		return nil
	}
	_, err := d.Exec(`DELETE FROM sessions WHERE id=?`, sid)
	return err
}

func DeleteSessionsForUser(d *sql.DB, userID int64) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

// PurgeExpiredSessions removes sessions whose expires_at is in the past.
func PurgeExpiredSessions(d *sql.DB) error {
	_, err := d.Exec(`DELETE FROM sessions WHERE expires_at::timestamptz < NOW()`)
	return err
}

// ----- request context helpers -----

type ctxKey int

const userKey ctxKey = 1

// FromContext returns the user attached to the request, or nil.
func FromContext(ctx context.Context) *User {
	if v, ok := ctx.Value(userKey).(*User); ok {
		return v
	}
	return nil
}

// WithUser returns a new context carrying u.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// SetSessionCookie writes the session cookie on the response.
func SetSessionCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(SessionDuration),
	})
}

// ClearSessionCookie expires the session cookie.
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// CookieValue returns the value of the session cookie, or "".
func CookieValue(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// LoadUser attempts to find the user for the request via its session cookie.
// Returns (nil, nil) when there is no valid session.
func LoadUser(d *sql.DB, r *http.Request) (*User, error) {
	sid := CookieValue(r)
	u, err := userBySession(d, sid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}
