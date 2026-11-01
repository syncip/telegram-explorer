// Package sftpd exposes the file tree over SFTP.
package sftpd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
)

// Server is the SFTP endpoint.
type Server struct {
	Addr   string
	Engine *files.Engine
	Auth   *auth.Manager
}

// hostKey loads the persistent key, so clients do not see a changed server
// after every restart. It is sealed because it identifies the server.
func (s *Server) hostKey() (ssh.Signer, error) {
	v := s.Engine.Vault()
	sealed, err := s.Engine.Store.Setting("sftp_host_key")
	if err != nil {
		return nil, err
	}
	if sealed != nil {
		raw, err := v.Open("sftp-host-key", sealed)
		if err != nil {
			return nil, err
		}
		return ssh.ParsePrivateKey(raw)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	raw := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if sealed, err = v.Seal("sftp-host-key", raw); err != nil {
		return nil, err
	}
	if err := s.Engine.Store.SetSetting("sftp_host_key", sealed); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(raw)
}

// Run serves until ctx ends. The vault must be unlocked.
func (s *Server) Run(ctx context.Context) error {
	signer, err := s.hostKey()
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			key := "sftp:" + hostOf(meta.RemoteAddr())
			if s.Auth.Limiter.Blocked(key) > 0 {
				return nil, errors.New("zu viele Fehlversuche")
			}
			if !s.Auth.CheckSFTP(meta.User(), string(pass)) {
				s.Auth.Limiter.Fail(key)
				return nil, auth.ErrInvalid
			}
			s.Auth.Limiter.Success(key)
			return &ssh.Permissions{}, nil
		},
	}
	cfg.AddHostKey(signer)
	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	log.Printf("SFTP lauscht auf %s", s.Addr)
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		go s.serve(ctx, conn, cfg)
	}
}

func hostOf(a net.Addr) string {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

func (s *Server) serve(ctx context.Context, conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	conn.SetDeadline(time.Time{})
	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			ch.Reject(ssh.UnknownChannelType, "nur SFTP")
			continue
		}
		channel, requests, err := ch.Accept()
		if err != nil {
			return
		}
		go func() {
			for req := range requests {
				ok := req.Type == "subsystem" && len(req.Payload) > 4 && string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
				if ok {
					s.session(ctx, channel)
					return
				}
			}
		}()
	}
}

func (s *Server) session(ctx context.Context, channel ssh.Channel) {
	defer channel.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	h := &handler{e: s.Engine, ctx: ctx}
	srv := sftp.NewRequestServer(channel, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h})
	defer srv.Close()
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("sftp: %v", err)
	}
}

type handler struct {
	e   *files.Engine
	ctx context.Context
}

// resolve walks an absolute path.
func (h *handler) resolve(p string) (*store.Node, error) {
	cur, err := h.e.Store.Get(store.RootID)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(path.Clean("/"+p), "/") {
		if part == "" {
			continue
		}
		if cur, err = h.e.Store.Child(cur.ID, part); err != nil {
			return nil, os.ErrNotExist
		}
	}
	return cur, nil
}

func (h *handler) parentOf(p string) (*store.Node, string, error) {
	clean := path.Clean("/" + p)
	parent, err := h.resolve(path.Dir(clean))
	if err != nil {
		return nil, "", err
	}
	if !parent.IsDir {
		return nil, "", os.ErrInvalid
	}
	return parent, path.Base(clean), nil
}

// Fileread implements sftp.FileReader.
func (h *handler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	n, err := h.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	return h.e.Open(h.ctx, n)
}

// Filewrite implements sftp.FileWriter. Telegram blobs are immutable, so data is
// collected on disk and committed as a new version when the client closes.
func (h *handler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	parent, name, err := h.parentOf(r.Filepath)
	if err != nil {
		return nil, err
	}
	if err := files.ValidName(name); err != nil {
		return nil, err
	}
	if existing, err := h.e.Store.Child(parent.ID, name); err == nil && existing.IsDir {
		return nil, os.ErrInvalid
	}
	f, err := h.e.NewStageFile()
	if err != nil {
		return nil, err
	}
	encrypted, err := h.e.Store.EncryptByDefault(parent.ID)
	if err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return &writer{File: f, h: h, parent: parent.ID, name: name, encrypted: encrypted}, nil
}

type writer struct {
	*os.File
	h         *handler
	parent    int64
	name      string
	encrypted bool
	once      sync.Once
	err       error
}

// Close commits the file exactly once.
func (w *writer) Close() error {
	w.once.Do(func() {
		if w.err = w.File.Close(); w.err != nil {
			os.Remove(w.File.Name())
			return
		}
		_, w.err = w.h.e.Commit(w.parent, w.name, w.File.Name(), w.encrypted, 0, true)
		if w.err != nil {
			os.Remove(w.File.Name())
		}
	})
	return w.err
}

// Filecmd implements sftp.FileCmder.
func (h *handler) Filecmd(r *sftp.Request) error {
	switch r.Method {
	case "Setstat":
		if attrs := r.Attributes(); r.AttrFlags().Acmodtime {
			if n, err := h.resolve(r.Filepath); err == nil {
				return h.e.Store.SetMTime(n.ID, int64(attrs.Mtime))
			}
		}
		return nil
	case "Mkdir":
		parent, name, err := h.parentOf(r.Filepath)
		if err != nil {
			return err
		}
		if err := files.ValidName(name); err != nil {
			return err
		}
		_, err = h.e.Store.Mkdir(parent.ID, name)
		return mapErr(err)
	case "Rename", "PosixRename":
		n, err := h.resolve(r.Filepath)
		if err != nil {
			return err
		}
		parent, name, err := h.parentOf(r.Target)
		if err != nil {
			return err
		}
		if err := files.ValidName(name); err != nil {
			return err
		}
		if r.Method == "PosixRename" {
			if old, err := h.e.Store.Child(parent.ID, name); err == nil && !old.IsDir && old.ID != n.ID {
				if err := h.e.Delete(old.ID); err != nil {
					return err
				}
			}
		}
		return mapErr(h.e.Store.Move(n.ID, parent.ID, name))
	case "Remove":
		n, err := h.resolve(r.Filepath)
		if err != nil {
			return err
		}
		if n.IsDir {
			return os.ErrInvalid
		}
		return h.e.Delete(n.ID)
	case "Rmdir":
		n, err := h.resolve(r.Filepath)
		if err != nil {
			return err
		}
		children, err := h.e.Store.List(n.ID)
		if err != nil {
			return err
		}
		if len(children) > 0 {
			return store.ErrNotEmpty
		}
		return h.e.Delete(n.ID)
	}
	return sftp.ErrSSHFxOpUnsupported
}

func mapErr(err error) error {
	if errors.Is(err, store.ErrExists) {
		return os.ErrExist
	}
	return err
}

// Filelist implements sftp.FileLister.
func (h *handler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	switch r.Method {
	case "List":
		n, err := h.resolve(r.Filepath)
		if err != nil {
			return nil, err
		}
		children, err := h.e.Store.List(n.ID)
		if err != nil {
			return nil, err
		}
		out := make(listAt, len(children))
		for i, c := range children {
			out[i] = info{c}
		}
		return out, nil
	case "Stat":
		n, err := h.resolve(r.Filepath)
		if err != nil {
			return nil, err
		}
		return listAt{info{n}}, nil
	case "Readlink":
		return nil, sftp.ErrSSHFxOpUnsupported
	}
	return nil, sftp.ErrSSHFxOpUnsupported
}

type listAt []os.FileInfo

func (l listAt) ListAt(dst []os.FileInfo, off int64) (int, error) {
	if off >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[off:])
	if n+int(off) >= len(l) {
		return n, io.EOF
	}
	return n, nil
}

type info struct{ n *store.Node }

func (i info) Name() string {
	if i.n.Name == "" {
		return "/"
	}
	return i.n.Name
}
func (i info) Size() int64        { return i.n.Size }
func (i info) ModTime() time.Time { return time.Unix(i.n.MTime, 0) }
func (i info) IsDir() bool        { return i.n.IsDir }
func (i info) Sys() any           { return nil }
func (i info) Mode() os.FileMode {
	if i.n.IsDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
