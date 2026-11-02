// Package web serves the JSON API, the public share pages and the UI.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/davd"
	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
	"telegram-explorer/internal/telegram"
	"telegram-explorer/internal/vault"
)

const (
	cookieName    = "telex_session"
	maxChunk      = 64 << 20
	maxJSON       = 1 << 16
	minPassword   = 8
	minPassphrase = 10
)

// TelegramLogin is the interactive part of the Telegram client.
type TelegramLogin interface {
	Status() telegram.Status
	SendCode(ctx context.Context, phone string) error
	SignIn(ctx context.Context, code string) error
	Password(ctx context.Context, password string) error
}

// Server holds the dependencies of all handlers.
type Server struct {
	Engine    *files.Engine
	Auth      *auth.Manager
	Telegram  TelegramLogin // nil with the local backend
	UI        fs.FS
	SetupCode string
	// TrustProxy honours X-Forwarded-* from a reverse proxy.
	TrustProxy bool
	// OnUnlock runs once the vault is open.
	OnUnlock func()
	// OnSFTPChange runs after the SFTP password was set or removed.
	OnSFTPChange func() error
	// WebDAV is nil when the endpoint is not wired up, as in tests.
	WebDAV *davd.Server
	// Bots is nil with the local backend.
	Bots HelperBots

	uploadLocks sync.Map
	grants      sync.Map // share password grant -> expiry
}

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(msg string) error { return httpError{http.StatusBadRequest, msg} }

type handler func(w http.ResponseWriter, r *http.Request) error

func writeJSON(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxJSON)).Decode(v); err != nil {
		return badRequest("ungültige Anfrage")
	}
	return nil
}

func (s *Server) wrap(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}
		code := http.StatusInternalServerError
		var he httpError
		switch {
		case errors.As(err, &he):
			code = he.code
		case errors.Is(err, store.ErrNotFound):
			code = http.StatusNotFound
		case errors.Is(err, store.ErrExists):
			code = http.StatusConflict
		case errors.Is(err, store.ErrCycle), errors.Is(err, store.ErrNotEmpty):
			code = http.StatusBadRequest
		case errors.Is(err, files.ErrLocked):
			code = http.StatusLocked
		case errors.Is(err, files.ErrNotReady):
			code = http.StatusServiceUnavailable
		case errors.Is(err, context.Canceled):
			return
		default:
			s.Engine.Store.Log(store.LevelError, store.KindSystem, "%s %s: %v", r.Method, r.URL.Path, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) secure(r *http.Request) bool {
	return r.TLS != nil || (s.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

// authed requires a session and, because every state change goes through it, a
// custom header that a cross-site form cannot send.
func (s *Server) authed(h handler) http.HandlerFunc {
	return s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		c, err := r.Cookie(cookieName)
		if err != nil || !s.Auth.ValidSession(c.Value) {
			return httpError{http.StatusUnauthorized, "nicht angemeldet"}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Telex") != "1" {
			return httpError{http.StatusForbidden, "CSRF-Prüfung fehlgeschlagen"}
		}
		return h(w, r)
	})
}

// open additionally requires an unlocked vault.
func (s *Server) open(h handler) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) error {
		if s.Engine.Vault() == nil {
			return files.ErrLocked
		}
		return h(w, r)
	})
}

// Handler builds the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/state", s.wrap(s.state))
	mux.HandleFunc("POST /api/setup", s.wrap(s.setup))
	mux.HandleFunc("POST /api/login", s.wrap(s.login))
	mux.HandleFunc("POST /api/logout", s.authed(s.logout))
	mux.HandleFunc("POST /api/unlock", s.authed(s.unlock))

	mux.HandleFunc("POST /api/telegram/phone", s.open(s.tgPhone))
	mux.HandleFunc("POST /api/telegram/code", s.open(s.tgCode))
	mux.HandleFunc("POST /api/telegram/password", s.open(s.tgPassword))

	mux.HandleFunc("GET /api/nodes/{id}", s.open(s.getNode))
	mux.HandleFunc("GET /api/nodes/{id}/children", s.open(s.children))
	mux.HandleFunc("GET /api/nodes/{id}/content", s.open(s.content))
	mux.HandleFunc("POST /api/nodes/{id}/mkdir", s.open(s.mkdir))
	mux.HandleFunc("PATCH /api/nodes/{id}", s.open(s.patchNode))
	mux.HandleFunc("POST /api/nodes/{id}/retry", s.open(s.retry))
	mux.HandleFunc("DELETE /api/nodes/{id}/shares", s.open(s.endSharesOf))
	mux.HandleFunc("POST /api/verify", s.open(s.startVerify))
	mux.HandleFunc("GET /api/verify", s.open(s.verifyState))
	mux.HandleFunc("DELETE /api/verify", s.open(s.clearVerify))
	mux.HandleFunc("GET /api/stats", s.open(s.stats))
	mux.HandleFunc("GET /api/logs", s.open(s.logs))
	mux.HandleFunc("DELETE /api/logs", s.open(s.clearLogs))
	mux.HandleFunc("POST /api/move", s.open(s.move))
	mux.HandleFunc("POST /api/delete", s.open(s.deleteNodes))
	mux.HandleFunc("GET /api/search", s.open(s.search))
	mux.HandleFunc("GET /api/zip", s.open(s.zipDownload))
	mux.HandleFunc("GET /api/jobs", s.open(s.jobs))

	mux.HandleFunc("GET /api/trash", s.open(s.trash))
	mux.HandleFunc("POST /api/trash/restore", s.open(s.restore))
	mux.HandleFunc("POST /api/trash/purge", s.open(s.purge))

	mux.HandleFunc("GET /api/nodes/{id}/uploads", s.open(s.folderUploads))
	mux.HandleFunc("POST /api/uploads", s.open(s.createUpload))
	mux.HandleFunc("GET /api/uploads/{id}", s.open(s.uploadStatus))
	mux.HandleFunc("PATCH /api/uploads/{id}", s.open(s.uploadChunk))
	mux.HandleFunc("DELETE /api/uploads/{id}", s.open(s.cancelUpload))

	mux.HandleFunc("GET /api/shares", s.open(s.listShares))
	mux.HandleFunc("POST /api/shares", s.open(s.createShare))
	mux.HandleFunc("PATCH /api/shares/{token}", s.open(s.updateShare))
	mux.HandleFunc("DELETE /api/shares/{token}", s.open(s.deleteShare))

	mux.HandleFunc("GET /api/settings", s.open(s.settings))
	mux.HandleFunc("POST /api/settings/password", s.open(s.setPassword))
	mux.HandleFunc("POST /api/settings/passphrase", s.open(s.setPassphrase))
	mux.HandleFunc("POST /api/settings/sftp", s.open(s.setSFTP))
	mux.HandleFunc("POST /api/settings/webdav", s.open(s.setWebDAV))
	mux.HandleFunc("GET /api/settings/webdav/cert", s.open(s.webdavCert))
	mux.HandleFunc("POST /api/settings/blocks", s.open(s.setBlocks))
	mux.HandleFunc("POST /api/settings/limits", s.open(s.setLimits))
	mux.HandleFunc("GET /api/settings/bots", s.open(s.listBots))
	mux.HandleFunc("POST /api/settings/bots", s.open(s.addBot))
	mux.HandleFunc("DELETE /api/settings/bots/{id}", s.open(s.removeBot))
	mux.HandleFunc("POST /api/settings/totp/begin", s.open(s.totpBegin))
	mux.HandleFunc("POST /api/settings/totp/confirm", s.open(s.totpConfirm))
	mux.HandleFunc("POST /api/settings/totp/disable", s.open(s.totpDisable))
	mux.HandleFunc("POST /api/backup", s.open(s.backupNow))

	mux.HandleFunc("GET /s/{token}", s.sharePage)
	mux.HandleFunc("POST /s/{token}", s.shareUnlock)
	mux.HandleFunc("GET /s/{token}/dl", s.shareDownload)

	mux.Handle("/", http.FileServerFS(s.UI))
	return headers(mux)
}

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) sessionCookie(w http.ResponseWriter, r *http.Request, token string, age int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.secure(r), SameSite: http.SameSiteStrictMode, MaxAge: age})
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) error {
	user, err := s.Auth.User()
	if err != nil {
		return err
	}
	out := map[string]any{"needsSetup": user == nil, "loggedIn": false, "locked": s.Engine.Vault() == nil}
	if c, err := r.Cookie(cookieName); err == nil && s.Auth.ValidSession(c.Value) {
		out["loggedIn"] = true
		out["username"] = user.Username
		if s.Telegram != nil && s.Engine.Vault() != nil {
			out["telegram"] = s.Telegram.Status()
		} else if s.Telegram == nil {
			out["telegram"] = telegram.Status{Step: telegram.StepReady, User: "Lokaler Testspeicher"}
		}
	}
	return writeJSON(w, out)
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	var in struct{ SetupCode, Username, Password, Passphrase string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	key := "setup:" + s.clientIP(r)
	if s.Auth.Limiter.Blocked(key) > 0 {
		return httpError{http.StatusTooManyRequests, "zu viele Fehlversuche"}
	}
	if user, err := s.Auth.User(); err != nil {
		return err
	} else if user != nil {
		return httpError{http.StatusConflict, "ist bereits eingerichtet"}
	}
	if in.SetupCode != s.SetupCode {
		s.Auth.Limiter.Fail(key)
		return httpError{http.StatusForbidden, "Einrichtungscode ist falsch (steht im Container-Log)"}
	}
	switch {
	case strings.TrimSpace(in.Username) == "":
		return badRequest("Benutzername fehlt")
	case len(in.Password) < minPassword:
		return badRequest("Kennwort braucht mindestens 8 Zeichen")
	case len(in.Passphrase) < minPassphrase:
		return badRequest("Passphrase braucht mindestens 10 Zeichen")
	}
	v, header, err := vault.Create(in.Passphrase)
	if err != nil {
		return err
	}
	if err := s.Engine.Store.SetSetting("vault", header); err != nil {
		return err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	if err := s.Auth.Save(&auth.User{Username: strings.TrimSpace(in.Username), Password: hash}); err != nil {
		return err
	}
	s.Engine.SetVault(v)
	s.OnUnlock()
	s.sessionCookie(w, r, s.Auth.NewSession(), int((7 * 24 * time.Hour).Seconds()))
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Username, Password, Code string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	key := "login:" + s.clientIP(r)
	if d := s.Auth.Limiter.Blocked(key); d > 0 {
		return httpError{http.StatusTooManyRequests, "zu viele Fehlversuche, bitte " + d.Round(time.Second).String() + " warten"}
	}
	token, err := s.Auth.Login(in.Username, in.Password, in.Code)
	if errors.Is(err, auth.ErrInvalid) {
		s.Auth.Limiter.Fail(key)
		s.Engine.Store.Log(store.LevelWarn, store.KindLogin, "Fehlgeschlagene Anmeldung von %s", s.clientIP(r))
		return httpError{http.StatusUnauthorized, err.Error()}
	}
	if err != nil {
		return err
	}
	s.Auth.Limiter.Success(key)
	s.Engine.Store.Log(store.LevelInfo, store.KindLogin, "Anmeldung von %s", s.clientIP(r))
	s.sessionCookie(w, r, token, int((7 * 24 * time.Hour).Seconds()))
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Auth.Logout(c.Value)
	}
	s.sessionCookie(w, r, "", -1)
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) unlock(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Passphrase string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	key := "unlock:" + s.clientIP(r)
	if d := s.Auth.Limiter.Blocked(key); d > 0 {
		return httpError{http.StatusTooManyRequests, "zu viele Fehlversuche, bitte " + d.Round(time.Second).String() + " warten"}
	}
	header, err := s.Engine.Store.Setting("vault")
	if err != nil {
		return err
	}
	v, err := vault.Unlock(header, in.Passphrase)
	if errors.Is(err, vault.ErrWrongPassphrase) {
		s.Auth.Limiter.Fail(key)
		return httpError{http.StatusUnauthorized, err.Error()}
	}
	if err != nil {
		return err
	}
	s.Auth.Limiter.Success(key)
	s.Engine.SetVault(v)
	s.OnUnlock()
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) tgLogin(fn func(TelegramLogin, map[string]string) error) handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		if s.Telegram == nil {
			return badRequest("lokaler Testspeicher aktiv")
		}
		var in map[string]string
		if err := readJSON(r, &in); err != nil {
			return err
		}
		if err := fn(s.Telegram, in); err != nil {
			return badRequest(err.Error())
		}
		return writeJSON(w, s.Telegram.Status())
	}
}

func (s *Server) tgPhone(w http.ResponseWriter, r *http.Request) error {
	return s.tgLogin(func(t TelegramLogin, in map[string]string) error {
		return t.SendCode(r.Context(), strings.ReplaceAll(in["phone"], " ", ""))
	})(w, r)
}

func (s *Server) tgCode(w http.ResponseWriter, r *http.Request) error {
	return s.tgLogin(func(t TelegramLogin, in map[string]string) error {
		return t.SignIn(r.Context(), strings.TrimSpace(in["code"]))
	})(w, r)
}

func (s *Server) tgPassword(w http.ResponseWriter, r *http.Request) error {
	return s.tgLogin(func(t TelegramLogin, in map[string]string) error {
		return t.Password(r.Context(), in["password"])
	})(w, r)
}

func pathID(r *http.Request, key string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil {
		return 0, badRequest("ungültige ID")
	}
	return id, nil
}

func (s *Server) liveNode(r *http.Request) (*store.Node, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	return s.Engine.Store.Get(id)
}
