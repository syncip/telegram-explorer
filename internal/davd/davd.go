// Package davd exposes the file tree over WebDAV with TLS.
package davd

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
)

// A password check costs a hash on purpose. WebDAV clients send the password
// with every request, so a good one is remembered for a while.
const goodLoginTTL = 10 * time.Minute

// Server is the WebDAV endpoint.
type Server struct {
	Addr     string
	Hosts    string // extra names for the self-signed certificate
	CertFile string // own certificate, replaces the self-signed one
	KeyFile  string
	Engine   *files.Engine
	Auth     *auth.Manager

	mu     sync.Mutex
	ctx    context.Context
	server *http.Server
	dav    *webdav.Handler

	loginMu sync.Mutex
	logins  map[[32]byte]time.Time
}

// Run serves until ctx ends. The port is only open while a WebDAV password is
// set. The vault must be unlocked.
func (s *Server) Run(ctx context.Context) error {
	s.mu.Lock()
	s.ctx = ctx
	s.dav = &webdav.Handler{FileSystem: &fileSystem{e: s.Engine}, LockSystem: webdav.NewMemLS(), Logger: func(r *http.Request, err error) {
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("WebDAV %s %s: %v", r.Method, r.URL.Path, err)
		}
	}}
	s.mu.Unlock()
	if err := s.Sync(); err != nil {
		return err
	}
	<-ctx.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop()
	return nil
}

// Sync opens or closes the port to match the settings. Closing it also ends
// running connections.
func (s *Server) Sync() error {
	enabled := s.Auth.WebDAVEnabled()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx == nil {
		return nil
	}
	if !enabled || s.ctx.Err() != nil {
		s.stop()
		return nil
	}
	if s.server != nil {
		return nil
	}
	pair, _, err := s.certificate()
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.server = &http.Server{
		Handler:           s,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	srv := s.server
	go func() {
		if err := srv.ServeTLS(l, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("WebDAV: %v", err)
		}
	}()
	log.Printf("WebDAV lauscht auf %s", s.Addr)
	return nil
}

func (s *Server) stop() {
	if s.server == nil {
		return
	}
	s.server.Close()
	s.server = nil
	s.loginMu.Lock()
	s.logins = nil
	s.loginMu.Unlock()
	log.Print("WebDAV beendet")
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(w, r) {
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		// Spares the handler from sniffing the content out of Telegram.
		w.Header().Set("Content-Type", files.MimeOf(r.URL.Path))
	}
	s.mu.Lock()
	dav := s.dav
	s.mu.Unlock()
	dav.ServeHTTP(w, r)
}

func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	key := "webdav:" + host
	if s.Auth.Limiter.Blocked(key) > 0 {
		http.Error(w, "zu viele Fehlversuche", http.StatusTooManyRequests)
		return false
	}
	user, pass, ok := r.BasicAuth()
	if ok && s.remembered(user, pass) {
		return true
	}
	if ok && s.Auth.CheckWebDAV(user, pass) {
		s.Auth.Limiter.Success(key)
		s.remember(user, pass)
		s.Engine.Store.Log(store.LevelInfo, store.KindLogin, "WebDAV-Anmeldung von %s", host)
		return true
	}
	if ok {
		// Clients first ask without a password; only a wrong one counts.
		s.Auth.Limiter.Fail(key)
		s.Engine.Store.Log(store.LevelWarn, store.KindLogin, "Fehlgeschlagene WebDAV-Anmeldung von %s", host)
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="Telegram Explorer", charset="UTF-8"`)
	http.Error(w, "Anmeldung erforderlich", http.StatusUnauthorized)
	return false
}

func loginKey(user, pass string) [32]byte {
	return sha256.Sum256([]byte(user + "\x00" + pass))
}

func (s *Server) remembered(user, pass string) bool {
	key := loginKey(user, pass)
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	for k, until := range s.logins {
		if subtle.ConstantTimeCompare(k[:], key[:]) == 1 && time.Now().Before(until) {
			return true
		}
	}
	return false
}

func (s *Server) remember(user, pass string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	if s.logins == nil {
		s.logins = map[[32]byte]time.Time{}
	}
	for k, until := range s.logins {
		if now.After(until) {
			delete(s.logins, k)
		}
	}
	s.logins[loginKey(user, pass)] = now.Add(goodLoginTTL)
}

// Forget drops remembered logins, for example after the password changed.
func (s *Server) Forget() {
	s.loginMu.Lock()
	s.logins = nil
	s.loginMu.Unlock()
}
