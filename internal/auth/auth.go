// Package auth manages the single user: credentials, TOTP, sessions and
// brute-force protection, shared by the web UI and SFTP.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"telegram-explorer/internal/store"
)

const (
	sessionTTL = 7 * 24 * time.Hour
	freeTries  = 5
	maxLock    = 15 * time.Minute
)

// ErrInvalid is the only answer to wrong credentials.
var ErrInvalid = errors.New("Anmeldedaten sind falsch")

// User is stored as one settings value.
type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	SFTP     string `json:"sftp,omitempty"`
	TOTP     string `json:"totp,omitempty"`
}

// Manager implements authentication.
type Manager struct {
	Store *store.Store
	// Limiter is shared by all login surfaces.
	Limiter *Limiter

	mu       sync.Mutex
	sessions map[string]time.Time
	pending  string // TOTP secret during enrollment
	lastStep int64  // last accepted TOTP step, against replay
}

// New creates a manager.
func New(s *store.Store) *Manager {
	return &Manager{Store: s, Limiter: NewLimiter(), sessions: map[string]time.Time{}}
}

// HashPassword derives an encoded Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 2, 64*1024, 2, 32)
	return hex.EncodeToString(salt) + "$" + hex.EncodeToString(hash), nil
}

// CheckHash verifies a password against HashPassword's output.
func CheckHash(encoded, password string) bool {
	saltHex, hashHex, ok := strings.Cut(encoded, "$")
	if !ok {
		return false
	}
	salt, err1 := hex.DecodeString(saltHex)
	want, err2 := hex.DecodeString(hashHex)
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 2, 64*1024, 2, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Token returns a random URL-safe token.
func Token(bytes int) string {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// User loads the account, or nil before setup.
func (m *Manager) User() (*User, error) {
	raw, err := m.Store.Setting("user")
	if err != nil || raw == nil {
		return nil, err
	}
	var u User
	return &u, json.Unmarshal(raw, &u)
}

// Save stores the account.
func (m *Manager) Save(u *User) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return m.Store.SetSetting("user", raw)
}

// Login checks the web credentials and returns a session token.
func (m *Manager) Login(username, password, code string) (string, error) {
	u, err := m.User()
	if err != nil {
		return "", err
	}
	if u == nil {
		return "", ErrInvalid
	}
	ok := CheckHash(u.Password, password)
	if subtle.ConstantTimeCompare([]byte(username), []byte(u.Username)) != 1 || !ok {
		return "", ErrInvalid
	}
	if u.TOTP != "" && !m.checkTOTP(u.TOTP, code, time.Now()) {
		return "", ErrInvalid
	}
	return m.NewSession(), nil
}

// NewSession starts a session without checking credentials.
func (m *Manager) NewSession() string {
	token := Token(32)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for t, exp := range m.sessions {
		if now.After(exp) {
			delete(m.sessions, t)
		}
	}
	m.sessions[token] = now.Add(sessionTTL)
	return token
}

// ValidSession reports whether a token belongs to a live session.
func (m *Manager) ValidSession(token string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.sessions[token]
	return ok && time.Now().Before(exp)
}

// Logout ends a session.
func (m *Manager) Logout(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
}

// LogoutAll ends every session.
func (m *Manager) LogoutAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = map[string]time.Time{}
}

// CheckSFTP validates SFTP credentials. SFTP has no second factor, so it uses
// its own password and stays closed until one is set.
func (m *Manager) CheckSFTP(username, password string) bool {
	u, err := m.User()
	if err != nil || u == nil || u.SFTP == "" {
		return false
	}
	ok := CheckHash(u.SFTP, password)
	return ok && subtle.ConstantTimeCompare([]byte(username), []byte(u.Username)) == 1
}

// BeginTOTP creates a secret to enroll.
func (m *Manager) BeginTOTP() (secret, uri string, err error) {
	u, err := m.User()
	if err != nil || u == nil {
		return "", "", errors.New("kein Benutzer")
	}
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	secret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	m.mu.Lock()
	m.pending = secret
	m.mu.Unlock()
	return secret, fmt.Sprintf("otpauth://totp/TelegramExplorer:%s?secret=%s&issuer=TelegramExplorer", u.Username, secret), nil
}

// ConfirmTOTP activates the pending secret once a code from the app matches.
func (m *Manager) ConfirmTOTP(code string) error {
	m.mu.Lock()
	secret := m.pending
	m.mu.Unlock()
	if secret == "" || !m.checkTOTP(secret, code, time.Now()) {
		return errors.New("Code ist falsch")
	}
	u, err := m.User()
	if err != nil {
		return err
	}
	u.TOTP = secret
	m.mu.Lock()
	m.pending = ""
	m.mu.Unlock()
	return m.Save(u)
}

func totpCode(secret string, step int64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000), nil
}

func (m *Manager) checkTOTP(secret, code string, now time.Time) bool {
	step := now.Unix() / 30
	for s := step - 1; s <= step+1; s++ {
		want, err := totpCode(secret, s)
		if err != nil || subtle.ConstantTimeCompare([]byte(want), []byte(code)) != 1 {
			continue
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if s <= m.lastStep {
			return false
		}
		m.lastStep = s
		return true
	}
	return false
}

// Limiter locks out a key with exponentially growing delays.
type Limiter struct {
	mu   sync.Mutex
	keys map[string]*entry
}

type entry struct {
	fails int
	until time.Time
	last  time.Time
}

// NewLimiter creates a limiter.
func NewLimiter() *Limiter { return &Limiter{keys: map[string]*entry{}} }

// Blocked returns how long a key must still wait.
func (l *Limiter) Blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.keys[key]; ok {
		return max(0, time.Until(e.until))
	}
	return 0
}

// Fail counts a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, e := range l.keys {
		if now.Sub(e.last) > time.Hour {
			delete(l.keys, k)
		}
	}
	e, ok := l.keys[key]
	if !ok {
		e = &entry{}
		l.keys[key] = e
	}
	e.fails++
	e.last = now
	if e.fails > freeTries {
		e.until = now.Add(min(time.Second<<min(e.fails-freeTries, 10), maxLock))
	}
}

// Success clears a key.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, key)
}
