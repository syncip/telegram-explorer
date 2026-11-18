package store

import (
	"database/sql"
	"errors"
)

// Session kinds.
const (
	SessionWeb = "web"
	SessionApp = "app"
)

// Session is a login of a browser or of the app. Only a hash of its token is
// stored, so a copy of the database does not log anybody in.
type Session struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	IP        string `json:"ip"`
	CreatedAt int64  `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen"`
	ExpiresAt int64  `json:"expiresAt"`
	Current   bool   `json:"current,omitempty"`
}

// CreateSession stores a session under the hash of its token.
func (s *Store) CreateSession(hash string, se *Session) error {
	_, err := s.quietExec(`INSERT INTO sessions(hash, id, kind, name, ip, created_at, last_seen, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hash, se.ID, se.Kind, se.Name, se.IP, se.CreatedAt, se.LastSeen, se.ExpiresAt)
	return err
}

// SessionByHash returns the session with this token hash.
func (s *Store) SessionByHash(hash string) (*Session, error) {
	var se Session
	err := s.db.QueryRow(`SELECT id, kind, name, ip, created_at, last_seen, expires_at FROM sessions WHERE hash = ?`, hash).
		Scan(&se.ID, &se.Kind, &se.Name, &se.IP, &se.CreatedAt, &se.LastSeen, &se.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &se, err
}

// TouchSession records a use and moves the expiry.
func (s *Store) TouchSession(id, ip string, now, expires int64) error {
	_, err := s.quietExec(`UPDATE sessions SET last_seen = ?, expires_at = ?, ip = ? WHERE id = ?`, now, expires, ip, id)
	return err
}

// Sessions lists the live sessions, most recently used first.
func (s *Store) Sessions(now int64) ([]*Session, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, ip, created_at, last_seen, expires_at FROM sessions WHERE expires_at > ? ORDER BY last_seen DESC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Session{}
	for rows.Next() {
		var se Session
		if err := rows.Scan(&se.ID, &se.Kind, &se.Name, &se.IP, &se.CreatedAt, &se.LastSeen, &se.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, &se)
	}
	return out, rows.Err()
}

// DeleteSession removes a session by its public id.
func (s *Store) DeleteSession(id string) error {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSessionByHash removes the session with this token hash.
func (s *Store) DeleteSessionByHash(hash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE hash = ?`, hash)
	return err
}

// DeleteSessions removes every session except the one with keepID.
func (s *Store) DeleteSessions(keepID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id != ?`, keepID)
	return err
}

// DeleteExpiredSessions removes sessions that ran out.
func (s *Store) DeleteExpiredSessions(now int64) error {
	_, err := s.quietExec(`DELETE FROM sessions WHERE expires_at <= ?`, now)
	return err
}
