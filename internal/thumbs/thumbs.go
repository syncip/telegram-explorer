// Package thumbs makes small JPEG previews of photos and videos and keeps them
// in a cache next to the database. Previews of encrypted files are encrypted
// with the vault, like the files themselves.
package thumbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"telegram-explorer/internal/store"
	"telegram-explorer/internal/vault"
)

// Size is the longest side of a preview in pixels.
const Size = 480

const (
	maxImageBytes = 60 << 20
	quality       = 80
	ffmpegTimeout = time.Minute
)

// ErrNone means a file has no preview.
var ErrNone = errors.New("keine Vorschau")

// ErrLocked means the vault is locked and the preview is encrypted.
var ErrLocked = errors.New("Tresor ist gesperrt")

// Source is an open file.
type Source interface {
	io.ReadSeeker
	io.ReaderAt
	io.Closer
}

// Maker creates and caches previews.
type Maker struct {
	Dir string
	// FFmpeg is the path of ffmpeg, or empty without video previews.
	FFmpeg string
	Vault  func() *vault.Vault
	Open   func(ctx context.Context, n *store.Node) (Source, error)

	once sync.Once
	sem  chan struct{}
	mu   sync.Mutex
	busy map[string]*call
}

type call struct {
	done chan struct{}
	data []byte
	err  error
}

// FindFFmpeg returns the path of ffmpeg, or "" when it is not installed.
func FindFFmpeg() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

func (m *Maker) init() {
	m.once.Do(func() {
		m.sem = make(chan struct{}, 2)
		m.busy = map[string]*call{}
		os.MkdirAll(m.Dir, 0o700)
	})
}

func isImage(n *store.Node) bool { return strings.HasPrefix(n.Mime, "image/") }
func isVideo(n *store.Node) bool { return strings.HasPrefix(n.Mime, "video/") }

// goDecodes lists the image types the standard library and x/image read.
func goDecodes(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/x-ms-bmp":
		return true
	}
	return false
}

// Supports reports whether a file can have a preview.
func (m *Maker) Supports(n *store.Node) bool {
	if m == nil || n.IsDir || n.Size == 0 || n.TrashedAt != 0 {
		return false
	}
	if goDecodes(n.Mime) {
		return n.Size <= maxImageBytes
	}
	if m.FFmpeg != "" && (isVideo(n) || isImage(n)) {
		return !strings.Contains(n.Mime, "svg")
	}
	return false
}

func key(n *store.Node) string {
	return fmt.Sprintf("%d-%d-%d-%d", n.ID, n.Size, n.MTime, n.CTime)
}

func (m *Maker) path(n *store.Node) string { return filepath.Join(m.Dir, key(n)+".jpg") }
func (m *Maker) none(n *store.Node) string { return filepath.Join(m.Dir, key(n)+".none") }

func purpose(n *store.Node) string { return fmt.Sprintf("thumb:%d", n.ID) }

// Get returns the JPEG preview of a file, making it on first use.
func (m *Maker) Get(ctx context.Context, n *store.Node) ([]byte, error) {
	if !m.Supports(n) {
		return nil, ErrNone
	}
	m.init()
	if data, err := m.cached(n); err == nil || !errors.Is(err, os.ErrNotExist) {
		return data, err
	}
	return m.make(ctx, n, "")
}

// FromFile makes the preview from a local copy of a file in the background,
// for example from the stage file of a fresh upload.
func (m *Maker) FromFile(n *store.Node, path string) {
	if !m.Supports(n) {
		return
	}
	m.init()
	go m.make(context.Background(), n, path)
}

// Remove drops the cached previews of a node.
func (m *Maker) Remove(id int64) {
	if m == nil {
		return
	}
	matches, _ := filepath.Glob(filepath.Join(m.Dir, fmt.Sprintf("%d-*", id)))
	for _, p := range matches {
		os.Remove(p)
	}
}

func (m *Maker) cached(n *store.Node) ([]byte, error) {
	if _, err := os.Stat(m.none(n)); err == nil {
		return nil, ErrNone
	}
	data, err := os.ReadFile(m.path(n))
	if err != nil {
		return nil, err
	}
	if !n.Encrypted {
		return data, nil
	}
	v := m.Vault()
	if v == nil {
		return nil, ErrLocked
	}
	return v.Open(purpose(n), data)
}

// make runs at most once per file at a time and at most twice in parallel.
func (m *Maker) make(ctx context.Context, n *store.Node, local string) ([]byte, error) {
	k := key(n)
	m.mu.Lock()
	if c, ok := m.busy[k]; ok {
		m.mu.Unlock()
		select {
		case <-c.done:
			return c.data, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	m.busy[k] = c
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, k)
		m.mu.Unlock()
		close(c.done)
	}()

	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		c.err = ctx.Err()
		return nil, c.err
	}
	if data, err := m.cached(n); err == nil || !errors.Is(err, os.ErrNotExist) {
		c.data, c.err = data, err
		return data, err
	}
	// A preview outlives the request that asked for it.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*ffmpegTimeout)
	defer cancel()
	c.data, c.err = m.render(work, n, local)
	if errors.Is(c.err, ErrNone) {
		os.WriteFile(m.none(n), nil, 0o600)
	}
	if c.err != nil {
		return nil, c.err
	}
	stored := c.data
	if n.Encrypted {
		v := m.Vault()
		if v == nil {
			return c.data, nil
		}
		if stored, c.err = v.Seal(purpose(n), c.data); c.err != nil {
			return nil, c.err
		}
	}
	tmp := m.path(n) + ".tmp"
	if err := os.WriteFile(tmp, stored, 0o600); err == nil {
		os.Rename(tmp, m.path(n))
	}
	return c.data, nil
}

func (m *Maker) render(ctx context.Context, n *store.Node, local string) ([]byte, error) {
	if goDecodes(n.Mime) {
		raw, err := m.readAll(ctx, n, local)
		if err != nil {
			return nil, err
		}
		if out, err := fromImage(raw); err == nil {
			return out, nil
		}
		if m.FFmpeg == "" {
			return nil, ErrNone
		}
	}
	if m.FFmpeg == "" {
		return nil, ErrNone
	}
	input := local
	if input == "" {
		src, err := m.Open(ctx, n)
		if err != nil {
			return nil, err
		}
		defer src.Close()
		url, stop, err := serve(src, time.Unix(n.MTime, 0))
		if err != nil {
			return nil, err
		}
		defer stop()
		input = url
	}
	// A frame one second in avoids black fade-ins; short clips use the first.
	for _, at := range []string{"1", "0"} {
		if !isVideo(n) && at == "1" {
			continue
		}
		out, err := m.ffmpeg(ctx, input, at)
		if err == nil && len(out) > 0 {
			return out, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, ErrNone
}

func (m *Maker) readAll(ctx context.Context, n *store.Node, local string) ([]byte, error) {
	if local != "" {
		return os.ReadFile(local)
	}
	src, err := m.Open(ctx, n)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return io.ReadAll(io.LimitReader(src, maxImageBytes))
}

func (m *Maker) ffmpeg(ctx context.Context, input, at string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()
	scale := fmt.Sprintf("scale='if(gt(iw,ih),min(%d,iw),-2)':'if(gt(iw,ih),-2,min(%d,ih))'", Size, Size)
	cmd := exec.CommandContext(ctx, m.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", at, "-i", input, "-frames:v", "1", "-vf", scale,
		"-f", "image2pipe", "-c:v", "mjpeg", "-q:v", "4", "-")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// serve makes a stored file readable for ffmpeg, which needs to seek in videos
// whose index sits at the end. It listens on loopback under a random path.
func serve(src Source, mtime time.Time) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	secret := fmt.Sprintf("/%d", time.Now().UnixNano())
	var mu sync.Mutex
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != secret {
			http.NotFound(w, r)
			return
		}
		// One reader serves all of ffmpeg's requests, one at a time.
		mu.Lock()
		defer mu.Unlock()
		http.ServeContent(w, r, "", mtime, src)
	})}
	go srv.Serve(ln)
	return "http://" + ln.Addr().String() + secret, func() { srv.Close() }, nil
}

func fromImage(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrNone
	}
	tw, th := w, h
	if w > Size || h > Size {
		if w >= h {
			tw, th = Size, max(1, h*Size/w)
		} else {
			tw, th = max(1, w*Size/h), Size
		}
	}
	// Halving first keeps the high-quality filter fast on large photos.
	src := image.Image(img)
	for src.Bounds().Dx() > tw*4 && src.Bounds().Dy() > th*4 {
		sb := src.Bounds()
		half := image.NewRGBA(image.Rect(0, 0, sb.Dx()/2, sb.Dy()/2))
		draw.ApproxBiLinear.Scale(half, half.Bounds(), src, sb, draw.Src, nil)
		src = half
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	out := orient(dst, exifOrientation(raw))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
