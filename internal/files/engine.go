// Package files turns a message store into a file system: it stages incoming
// data on disk, pushes it to the backend in the background and reads it back.
package files

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"telegram-explorer/internal/store"
	"telegram-explorer/internal/vault"
)

const (
	// PartSize is the window size of Backend.ReadPart.
	PartSize = 1 << 20
	// DefaultBlockParts makes blocks 1 GiB large.
	DefaultBlockParts = 1024
	// DirectLimit is the largest file Telegram keeps as a single document on a
	// free account. Unencrypted files up to here stay directly downloadable.
	DirectLimit = 2000 << 20

	trashRetention  = 30 * 24 * time.Hour
	uploadRetention = 7 * 24 * time.Hour
	backupsKept     = 5
)

// ErrLocked is returned while the vault is locked.
var ErrLocked = errors.New("Tresor ist gesperrt")

// ErrNotReady is returned while the backend is not connected.
var ErrNotReady = errors.New("Telegram ist nicht verbunden")

// ErrMissing is returned when a message is gone from the backend.
var ErrMissing = errors.New("Daten fehlen im Speicherkanal")

// Backend stores opaque blobs as messages.
type Backend interface {
	Ready() bool
	Put(ctx context.Context, name, mimeType string, r io.Reader, size int64, progress func(sent int64)) (msgID int, err error)
	// ReadPart returns up to PartSize bytes from offset part*PartSize. Only the
	// last part of a blob may be shorter.
	ReadPart(ctx context.Context, msgID int, part int64) ([]byte, error)
	Delete(ctx context.Context, msgIDs []int) error
}

// Engine implements file operations on top of a store and a backend.
type Engine struct {
	Store      *store.Store
	Backend    Backend
	Dir        string
	BlockParts int

	vault    atomic.Pointer[vault.Vault]
	wake     chan struct{}
	clean    chan struct{}
	progress sync.Map // node id -> *atomic.Int64
	started  atomic.Bool
	verifier verifier
	ctx      context.Context
}

// New creates an engine that keeps stage files below dataDir.
func New(s *store.Store, b Backend, dataDir string) (*Engine, error) {
	dir := filepath.Join(dataDir, "stage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Engine{Store: s, Backend: b, Dir: dir, BlockParts: DefaultBlockParts, ctx: context.Background(),
		wake: make(chan struct{}, 1), clean: make(chan struct{}, 1)}, nil
}

// SetVault installs the unlocked vault.
func (e *Engine) SetVault(v *vault.Vault) { e.vault.Store(v) }

// Vault returns the unlocked vault, or nil while locked.
func (e *Engine) Vault() *vault.Vault { return e.vault.Load() }

// ValidName rejects names that cannot exist in the tree.
func ValidName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return errors.New("ungültiger Name")
	case len(name) > 255:
		return errors.New("Name ist zu lang")
	case strings.ContainsAny(name, "/\\\x00"):
		return errors.New(`Name darf weder "/" noch "\" enthalten`)
	}
	return nil
}

// NewStageFile creates an empty file for incoming data.
func (e *Engine) NewStageFile() (*os.File, error) {
	return os.CreateTemp(e.Dir, "in-*.tmp")
}

// StagePath returns the stage file of a resumable upload.
func (e *Engine) StagePath(uploadID string) string {
	return filepath.Join(e.Dir, "up-"+uploadID+".tmp")
}

// Commit turns a complete stage file into a file node and queues its upload.
// The stage file belongs to the engine afterwards.
func (e *Engine) Commit(parent int64, name, stagePath string, encrypted bool, mtime int64, replace bool) (*store.Node, error) {
	if err := ValidName(name); err != nil {
		return nil, err
	}
	v := e.Vault()
	if v == nil {
		return nil, ErrLocked
	}
	info, err := os.Stat(stagePath)
	if err != nil {
		return nil, err
	}
	sha, md, err := hashFile(stagePath)
	if err != nil {
		return nil, err
	}
	if mtime == 0 {
		mtime = time.Now().Unix()
	}
	n := &store.Node{ParentID: parent, Name: name, Size: info.Size(), Mime: MimeOf(name), Encrypted: encrypted,
		State: store.StatePending, SHA256: sha, MD5: md, StagePath: stagePath, MTime: mtime}
	if n.Size == 0 {
		n.State, n.StagePath = store.StateReady, ""
	}
	if encrypted {
		if n.WrappedKey, err = v.NewFileKey(); err != nil {
			return nil, err
		}
	}
	created, err := e.Store.InsertFile(n, replace)
	if err != nil {
		return nil, err
	}
	if n.Size == 0 {
		os.Remove(stagePath)
	}
	e.Wake()
	return created, nil
}

func hashFile(path string) (sha, md string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	s, m := sha256.New(), md5.New()
	if _, err := io.Copy(io.MultiWriter(s, m), f); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(s.Sum(nil)), hex.EncodeToString(m.Sum(nil)), nil
}

// MimeOf guesses a content type from the file name.
func MimeOf(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// Wake makes the upload worker look for work now.
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Start launches the background workers. It is safe to call it more than once.
func (e *Engine) Start(ctx context.Context) {
	if !e.started.CompareAndSwap(false, true) {
		return
	}
	e.ctx = ctx
	e.cleanStage()
	go e.uploadLoop(ctx)
	go e.janitorLoop(ctx)
}

// cleanStage removes leftovers from a crash that nothing refers to anymore.
func (e *Engine) cleanStage() {
	used, err := e.Store.StagePaths()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(e.Dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(e.Dir, entry.Name())
		if strings.HasPrefix(entry.Name(), "in-") && !used[path] {
			os.Remove(path)
		}
	}
}

func (e *Engine) uploadLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for e.Backend.Ready() && e.Vault() != nil {
			n, err := e.Store.NextPending(time.Now().Unix())
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					log.Printf("upload queue: %v", err)
				}
				break
			}
			e.runUpload(ctx, n)
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-e.wake:
		case <-ticker.C:
		}
	}
}

func (e *Engine) runUpload(ctx context.Context, n *store.Node) {
	err := e.upload(ctx, n)
	e.progress.Delete(n.ID)
	switch {
	case err == nil:
		e.Store.Log(store.LevelInfo, store.KindUpload, "„%s“ zu Telegram übertragen (%s)", n.Name, protection(n))
	case ctx.Err() != nil, errors.Is(err, store.ErrNotFound):
	case errors.Is(err, os.ErrNotExist):
		e.Store.Fail(n.ID, "Zwischenspeicher-Datei fehlt, bitte erneut hochladen")
		e.Store.Log(store.LevelError, store.KindUpload, "„%s“: Zwischenspeicher-Datei fehlt", n.Name)
	default:
		delay := time.Duration(1<<min(n.Attempts, 9)) * 15 * time.Second
		if delay > time.Hour {
			delay = time.Hour
		}
		e.Store.Log(store.LevelWarn, store.KindUpload, "„%s“: %v, neuer Versuch in %s", n.Name, err, delay)
		e.Store.Retry(n.ID, time.Now().Add(delay).Unix(), err.Error())
	}
}

func protection(n *store.Node) string {
	if n.Encrypted {
		return "verschlüsselt"
	}
	return "unverschlüsselt"
}

// layout describes how a file is cut into blocks.
type layout struct {
	segPlain   int64 // plaintext per part
	blockParts int64 // parts per block
	blocks     int
}

func (e *Engine) layoutOf(size int64, encrypted bool) layout {
	l := layout{segPlain: PartSize, blockParts: int64(e.BlockParts)}
	if encrypted {
		l.segPlain = vault.SegmentPlain
	} else if size <= DirectLimit && size <= int64(e.BlockParts)*2*PartSize {
		// Keep it one message so it stays a normal file in Telegram.
		l.blockParts = max(1, ceilDiv(size, PartSize))
	}
	l.blocks = int(ceilDiv(ceilDiv(size, l.segPlain), l.blockParts))
	return l
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func (e *Engine) upload(ctx context.Context, n *store.Node) error {
	f, err := os.Open(n.StagePath)
	if err != nil {
		return err
	}
	defer f.Close()
	done, err := e.Store.Blocks(n.ID)
	if err != nil {
		return err
	}
	l := e.layoutOf(n.Size, n.Encrypted)
	var fc *vault.FileCipher
	if n.Encrypted {
		v := e.Vault()
		if v == nil {
			return ErrLocked
		}
		if fc, err = v.FileCipher(n.WrappedKey, n.Size); err != nil {
			return err
		}
	}
	sent := &atomic.Int64{}
	e.progress.Store(n.ID, sent)
	var finished int64
	for _, b := range done {
		finished += b.Size
	}
	sent.Store(finished)

	for idx := len(done); idx < l.blocks; idx++ {
		start := int64(idx) * l.blockParts * l.segPlain
		length := min(l.blockParts*l.segPlain, n.Size-start)
		name, r, size := blockSource(f, n, l, idx, start, length, fc)
		msgID, err := e.Backend.Put(ctx, name, blockMime(n), r, size, func(s int64) { sent.Store(finished + s) })
		if err != nil {
			return fmt.Errorf("Block %d: %w", idx, err)
		}
		if _, err := e.Store.Get(n.ID); err != nil {
			// Purged while the block was in flight.
			e.Store.AddOrphan(msgID)
			return err
		}
		if err := e.Store.AddBlock(n.ID, store.Block{Index: idx, MsgID: msgID, Size: size}); err != nil {
			e.Store.AddOrphan(msgID)
			return err
		}
		finished += size
		sent.Store(finished)
	}
	if err := e.Store.MarkReady(n.ID); err != nil {
		return err
	}
	f.Close()
	os.Remove(n.StagePath)
	return nil
}

func blockMime(n *store.Node) string {
	if n.Encrypted {
		return "application/octet-stream"
	}
	return n.Mime
}

func blockSource(f io.ReaderAt, n *store.Node, l layout, idx int, start, length int64, fc *vault.FileCipher) (string, io.Reader, int64) {
	if fc == nil {
		name := n.Name
		if l.blocks > 1 {
			name = fmt.Sprintf("%s.part%03d", n.Name, idx+1)
		}
		return name, io.NewSectionReader(f, start, length), length
	}
	var id [16]byte
	rand.Read(id[:])
	segments := ceilDiv(length, vault.SegmentPlain)
	return hex.EncodeToString(id[:]) + ".bin",
		&sealingReader{src: f, fc: fc, seg: int64(idx) * l.blockParts, off: start, remaining: length},
		length + segments*vault.Overhead
}

// sealingReader encrypts a plaintext range segment by segment.
type sealingReader struct {
	src       io.ReaderAt
	fc        *vault.FileCipher
	seg, off  int64
	remaining int64
	plain     []byte
	buf       []byte
}

func (r *sealingReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		if r.remaining == 0 {
			return 0, io.EOF
		}
		if r.plain == nil {
			r.plain = make([]byte, vault.SegmentPlain)
		}
		n := min(int64(vault.SegmentPlain), r.remaining)
		if _, err := r.src.ReadAt(r.plain[:n], r.off); err != nil {
			return 0, err
		}
		r.buf = r.fc.SealSegment(make([]byte, 0, n+vault.Overhead), r.seg, r.plain[:n])
		r.seg++
		r.off += n
		r.remaining -= n
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// JobInfo is an upload visible in the UI.
type JobInfo struct {
	*store.Node
	Sent  int64 `json:"sent"`
	Total int64 `json:"total"`
}

// Jobs lists uploads that have not finished.
func (e *Engine) Jobs() ([]JobInfo, error) {
	nodes, err := e.Store.Jobs()
	if err != nil {
		return nil, err
	}
	out := make([]JobInfo, 0, len(nodes))
	for _, n := range nodes {
		job := JobInfo{Node: n, Total: n.Size}
		if p, ok := e.progress.Load(n.ID); ok {
			job.Sent = min(p.(*atomic.Int64).Load(), n.Size)
		}
		out = append(out, job)
	}
	return out, nil
}

// Delete moves a node to the trash.
func (e *Engine) Delete(id int64) error {
	n, err := e.Store.Get(id)
	if err != nil {
		return err
	}
	if err := e.Store.Trash(id); err != nil {
		return err
	}
	e.Store.Log(store.LevelInfo, store.KindDelete, "„%s“ in den Papierkorb verschoben", n.Name)
	return nil
}

// Purge deletes a trashed node for good. The Telegram messages go right away:
// nobody expects a permanently deleted file to linger until the next cleanup.
func (e *Engine) Purge(id int64) error {
	n, err := e.Store.Get(id)
	if err != nil {
		return err
	}
	stages, err := e.Store.Purge(id)
	if err != nil {
		return err
	}
	for _, p := range stages {
		os.Remove(p)
	}
	e.Store.Log(store.LevelInfo, store.KindDelete, "„%s“ endgültig gelöscht", n.Name)
	select {
	case e.clean <- struct{}{}:
	default:
	}
	return nil
}

func (e *Engine) janitorLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	lastBackup := time.Time{}
	var backedUp int64 = -1
	for {
		e.janitor(ctx)
		// Identical backups would only push older, useful ones out of rotation.
		changes, err := e.Store.Changes()
		if err == nil && changes != backedUp && time.Since(lastBackup) > 6*time.Hour && e.Backend.Ready() && e.Vault() != nil {
			if err := e.Backup(ctx); err != nil {
				e.Store.Log(store.LevelError, store.KindSystem, "Backup fehlgeschlagen: %v", err)
			} else {
				lastBackup = time.Now()
				backedUp, _ = e.Store.Changes()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-e.clean:
		}
	}
}

func (e *Engine) janitor(ctx context.Context) {
	if old, err := e.Store.PurgeBefore(time.Now().Add(-trashRetention).Unix()); err == nil {
		for _, n := range old {
			if err := e.Purge(n.ID); err != nil {
				log.Printf("purge %d: %v", n.ID, err)
			}
		}
	}
	if stale, err := e.Store.StaleUploads(time.Now().Add(-uploadRetention).Unix()); err == nil {
		for _, id := range stale {
			e.Store.DeleteUpload(id)
			os.Remove(e.StagePath(id))
		}
	}
	if !e.Backend.Ready() {
		return
	}
	for {
		ids, err := e.Store.Orphans(100)
		if err != nil || len(ids) == 0 {
			return
		}
		if err := e.Backend.Delete(ctx, ids); err != nil {
			e.Store.Log(store.LevelError, store.KindDelete, "%d Nachricht(en) konnten bei Telegram nicht gelöscht werden: %v", len(ids), err)
			return
		}
		e.Store.RemoveOrphans(ids)
		e.Store.Log(store.LevelInfo, store.KindDelete, "%d Nachricht(en) bei Telegram gelöscht", len(ids))
	}
}

// Backup uploads an encrypted database snapshot if anything changed.
func (e *Engine) Backup(ctx context.Context) error {
	v := e.Vault()
	if v == nil {
		return ErrLocked
	}
	if !e.Backend.Ready() {
		return ErrNotReady
	}
	tmp := filepath.Join(e.Dir, fmt.Sprintf("snapshot-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := e.Store.Snapshot(tmp); err != nil {
		return err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	header, err := e.Store.Setting("vault")
	if err != nil {
		return err
	}
	sealed, err := v.SealBackup(header, data)
	if err != nil {
		return err
	}
	name := "telex-backup-" + time.Now().UTC().Format("20060102-150405") + ".telexbk"
	msgID, err := e.Backend.Put(ctx, name, "application/octet-stream", bytes.NewReader(sealed), int64(len(sealed)), nil)
	if err != nil {
		return err
	}
	var kept []int
	if raw, _ := e.Store.Setting("backups"); raw != nil {
		json.Unmarshal(raw, &kept)
	}
	kept = append(kept, msgID)
	for len(kept) > backupsKept {
		if err := e.Backend.Delete(ctx, kept[:1]); err != nil {
			e.Store.AddOrphan(kept[0])
		}
		kept = kept[1:]
	}
	raw, _ := json.Marshal(kept)
	if err := e.Store.SetSetting("backups", raw); err != nil {
		return err
	}
	return e.Store.SetSetting("backup_at", []byte(time.Now().UTC().Format(time.RFC3339)))
}
