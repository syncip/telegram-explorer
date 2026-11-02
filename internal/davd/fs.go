package davd

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
)

// fileSystem maps WebDAV onto the file tree. Telegram blobs never change, so
// writes are collected on disk and committed as a new version on close.
type fileSystem struct {
	e *files.Engine
}

func (fs *fileSystem) resolve(p string) (*store.Node, error) {
	cur, err := fs.e.Store.Get(store.RootID)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(path.Clean("/"+p), "/") {
		if part == "" {
			continue
		}
		if cur, err = fs.e.Store.Child(cur.ID, part); err != nil {
			return nil, os.ErrNotExist
		}
	}
	return cur, nil
}

func (fs *fileSystem) parentOf(p string) (*store.Node, string, error) {
	clean := path.Clean("/" + p)
	if clean == "/" {
		return nil, "", os.ErrInvalid
	}
	parent, err := fs.resolve(path.Dir(clean))
	if err != nil {
		return nil, "", err
	}
	if !parent.IsDir {
		return nil, "", os.ErrInvalid
	}
	name := path.Base(clean)
	if err := files.ValidName(name); err != nil {
		return nil, "", os.ErrInvalid
	}
	return parent, name, nil
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, store.ErrExists):
		return os.ErrExist
	case errors.Is(err, store.ErrNotFound):
		return os.ErrNotExist
	}
	return err
}

func (fs *fileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	parent, base, err := fs.parentOf(name)
	if err != nil {
		return err
	}
	_, err = fs.e.Store.Mkdir(parent.ID, base)
	return mapErr(err)
}

func (fs *fileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		return fs.create(name, flag)
	}
	n, err := fs.resolve(name)
	if err != nil {
		return nil, err
	}
	if n.IsDir {
		return &dir{fs: fs, n: n}, nil
	}
	// Opening is cheap; the content is only fetched once something is read.
	return &readFile{fs: fs, n: n, ctx: ctx}, nil
}

func (fs *fileSystem) create(name string, flag int) (webdav.File, error) {
	parent, base, err := fs.parentOf(name)
	if err != nil {
		return nil, err
	}
	existing, err := fs.e.Store.Child(parent.ID, base)
	switch {
	case err == nil && existing.IsDir:
		return nil, os.ErrInvalid
	case err != nil && flag&os.O_CREATE == 0:
		return nil, os.ErrNotExist
	case err == nil && flag&os.O_TRUNC == 0:
		// Changing part of a stored file would mean fetching it first.
		return nil, os.ErrPermission
	}
	encrypted, err := fs.e.Store.EncryptByDefault(parent.ID)
	if err != nil {
		return nil, err
	}
	f, err := fs.e.NewStageFile()
	if err != nil {
		return nil, err
	}
	return &writeFile{File: f, fs: fs, parent: parent.ID, name: base, encrypted: encrypted}, nil
}

func (fs *fileSystem) RemoveAll(ctx context.Context, name string) error {
	n, err := fs.resolve(name)
	if err != nil {
		return err
	}
	if n.ID == store.RootID {
		return os.ErrPermission
	}
	return fs.e.Delete(n.ID)
}

func (fs *fileSystem) Rename(ctx context.Context, oldName, newName string) error {
	n, err := fs.resolve(oldName)
	if err != nil {
		return err
	}
	if n.ID == store.RootID {
		return os.ErrPermission
	}
	parent, base, err := fs.parentOf(newName)
	if err != nil {
		return err
	}
	return mapErr(fs.e.Store.Move(n.ID, parent.ID, base))
}

func (fs *fileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	n, err := fs.resolve(name)
	if err != nil {
		return nil, err
	}
	return info{n}, nil
}

// readFile opens the stored content on the first read.
type readFile struct {
	fs  *fileSystem
	n   *store.Node
	ctx context.Context

	mu     sync.Mutex
	r      *files.Reader
	offset int64
	logged bool
}

func (f *readFile) reader() (*files.Reader, error) {
	if f.r == nil {
		r, err := f.fs.e.Open(f.ctx, f.n)
		if err != nil {
			return nil, err
		}
		if _, err := r.Seek(f.offset, io.SeekStart); err != nil {
			r.Close()
			return nil, err
		}
		f.r = r
	}
	if !f.logged {
		f.logged = true
		f.fs.e.Store.Log(store.LevelInfo, store.KindDownload, "„%s“ per WebDAV geöffnet", f.n.Name)
	}
	return f.r, nil
}

func (f *readFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.reader()
	if err != nil {
		return 0, err
	}
	n, err := r.Read(p)
	f.offset += int64(n)
	return n, err
}

func (f *readFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch whence {
	case io.SeekCurrent:
		offset += f.offset
	case io.SeekEnd:
		offset += f.n.Size
	}
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	f.offset = offset
	if f.r != nil {
		return f.r.Seek(offset, io.SeekStart)
	}
	return offset, nil
}

func (f *readFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.r != nil {
		return f.r.Close()
	}
	return nil
}

func (f *readFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }
func (f *readFile) Stat() (os.FileInfo, error)         { return info{f.n}, nil }
func (f *readFile) Write([]byte) (int, error)          { return 0, os.ErrPermission }

type dir struct {
	fs   *fileSystem
	n    *store.Node
	list []os.FileInfo
	read bool
}

func (d *dir) Readdir(count int) ([]os.FileInfo, error) {
	if !d.read {
		children, err := d.fs.e.Store.List(d.n.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			d.list = append(d.list, info{c})
		}
		d.read = true
	}
	if count <= 0 {
		out := d.list
		d.list = nil
		return out, nil
	}
	if len(d.list) == 0 {
		return nil, io.EOF
	}
	n := min(count, len(d.list))
	out := d.list[:n]
	d.list = d.list[n:]
	return out, nil
}

func (d *dir) Read([]byte) (int, error)       { return 0, os.ErrInvalid }
func (d *dir) Seek(int64, int) (int64, error) { return 0, nil }
func (d *dir) Write([]byte) (int, error)      { return 0, os.ErrInvalid }
func (d *dir) Close() error                   { return nil }
func (d *dir) Stat() (os.FileInfo, error)     { return info{d.n}, nil }

type writeFile struct {
	*os.File
	fs        *fileSystem
	parent    int64
	name      string
	encrypted bool
	once      sync.Once
	err       error
}

// Stat describes the new file, not the stage file behind it.
func (w *writeFile) Stat() (os.FileInfo, error) {
	st, err := w.File.Stat()
	if err != nil {
		return nil, err
	}
	return info{&store.Node{Name: w.name, Size: st.Size(), MTime: time.Now().Unix(), Mime: files.MimeOf(w.name)}}, nil
}

// Close commits the file exactly once.
func (w *writeFile) Close() error {
	w.once.Do(func() {
		if w.err = w.File.Close(); w.err != nil {
			os.Remove(w.File.Name())
			return
		}
		_, w.err = w.fs.e.Commit(w.parent, w.name, w.File.Name(), w.encrypted, 0, true)
		if w.err != nil {
			os.Remove(w.File.Name())
			w.fs.e.Store.Log(store.LevelError, store.KindUpload, "WebDAV-Upload „%s“ fehlgeschlagen: %v", w.name, w.err)
			return
		}
		w.fs.e.Store.Log(store.LevelInfo, store.KindUpload, "„%s“ per WebDAV hochgeladen", w.name)
	})
	return w.err
}

func (w *writeFile) Readdir(int) ([]os.FileInfo, error) { return nil, os.ErrInvalid }

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

// ContentType keeps the handler from reading the content to guess it.
func (i info) ContentType(context.Context) (string, error) {
	if i.n.IsDir {
		return "", webdav.ErrNotImplemented
	}
	return i.n.Mime, nil
}

// ETag changes whenever a file is replaced or touched.
func (i info) ETag(context.Context) (string, error) {
	return `"` + strconv.FormatInt(i.n.ID, 36) + "-" + strconv.FormatInt(i.n.MTime, 36) + "-" + strconv.FormatInt(i.n.Size, 36) + `"`, nil
}
