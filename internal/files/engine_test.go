package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"telegram-explorer/internal/store"
	"telegram-explorer/internal/vault"
)

type flakyBackend struct {
	*LocalBackend
	fails int
}

func (b *flakyBackend) Put(ctx context.Context, name, mime string, r io.Reader, size int64, p func(int64)) (int, error) {
	if b.fails > 0 {
		b.fails--
		return 0, errors.New("simulated network failure")
	}
	return b.LocalBackend.Put(ctx, name, mime, r, size, p)
}

func setup(t *testing.T, backend Backend) *Engine {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if backend == nil {
		b, err := NewLocalBackend(filepath.Join(dir, "blobs"))
		if err != nil {
			t.Fatal(err)
		}
		backend = b
	}
	e, err := New(s, backend, dir)
	if err != nil {
		t.Fatal(err)
	}
	e.BlockParts = 4
	v, _, err := vault.Create("pw")
	if err != nil {
		t.Fatal(err)
	}
	e.SetVault(v)
	return e
}

func put(t *testing.T, e *Engine, name string, data []byte, encrypted bool) *store.Node {
	t.Helper()
	f, err := e.NewStageFile()
	if err != nil {
		t.Fatal(err)
	}
	f.Write(data)
	f.Close()
	n, err := e.Commit(store.RootID, name, f.Name(), encrypted, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type failingBackend struct {
	*LocalBackend
	puts int
}

func (b *failingBackend) Put(ctx context.Context, name, mime string, r io.Reader, size int64, progress func(int64)) (int, error) {
	if b.puts++; b.puts == 2 {
		return 0, errors.New("abgebrochen")
	}
	return b.LocalBackend.Put(ctx, name, mime, r, size, progress)
}

func TestBlockSizeChange(t *testing.T) {
	local, err := NewLocalBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := setup(t, &failingBackend{LocalBackend: local})
	ctx := context.Background()
	data := make([]byte, 9*PartSize)
	rand.Read(data)

	// An upload that broke off keeps its block size when the setting changes.
	resumed := put(t, e, "alt.bin", data, true)
	if err := e.upload(ctx, resumed); err == nil {
		t.Fatal("expected the second block to fail")
	}
	if err := e.SetBlockMiB(2); err != nil {
		t.Fatal(err)
	}
	if err := e.upload(ctx, resumed); err != nil {
		t.Fatal(err)
	}
	e.Store.MarkReady(resumed.ID)

	// Files stored with the old size stay readable, new ones use the new size.
	fresh := put(t, e, "neu.bin", data, true)
	if err := e.upload(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	e.Store.MarkReady(fresh.ID)
	for id, want := range map[int64]int{resumed.ID: 3, fresh.ID: 5} {
		blocks, _ := e.Store.Blocks(id)
		if len(blocks) != want {
			t.Fatalf("node %d: got %d blocks, want %d", id, len(blocks), want)
		}
		n, _ := e.Store.Get(id)
		if err := e.Verify(ctx, n); err != nil {
			t.Fatalf("node %d: %v", id, err)
		}
	}
}

func TestRoundTripAndRandomAccess(t *testing.T) {
	sizes := []int{1, vault.SegmentPlain - 1, vault.SegmentPlain, vault.SegmentPlain + 1, 4*vault.SegmentPlain + 7, 9*PartSize + 3}
	for _, encrypted := range []bool{true, false} {
		for _, size := range sizes {
			e := setup(t, nil)
			data := make([]byte, size)
			rand.Read(data)
			n := put(t, e, "f.bin", data, encrypted)

			// Readable from the stage file before the upload ran.
			r, err := e.Open(context.Background(), n)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(r)
			r.Close()
			if !bytes.Equal(got, data) {
				t.Fatalf("stage read differs, size=%d", size)
			}

			if err := e.upload(context.Background(), n); err != nil {
				t.Fatalf("upload size=%d enc=%v: %v", size, encrypted, err)
			}
			e.Store.MarkReady(n.ID)
			n, _ = e.Store.Get(n.ID)
			if _, err := os.Stat(e.Dir); err != nil {
				t.Fatal(err)
			}
			if err := e.Verify(context.Background(), n); err != nil {
				t.Fatalf("verify size=%d enc=%v: %v", size, encrypted, err)
			}

			r, err = e.Open(context.Background(), n)
			if err != nil {
				t.Fatal(err)
			}
			got, err = io.ReadAll(r)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("read differs size=%d enc=%v err=%v", size, encrypted, err)
			}
			if size > 10 {
				buf := make([]byte, 10)
				off := int64(size - 10)
				if _, err := r.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
					t.Fatal(err)
				}
				if !bytes.Equal(buf, data[off:]) {
					t.Fatalf("range read differs size=%d", size)
				}
			}
			r.Close()
		}
	}
}

func TestEmptyFile(t *testing.T) {
	e := setup(t, nil)
	n := put(t, e, "empty", nil, true)
	if n.State != store.StateReady {
		t.Fatalf("state = %s", n.State)
	}
	r, err := e.Open(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if b, err := io.ReadAll(r); err != nil || len(b) != 0 {
		t.Fatalf("got %d bytes, err %v", len(b), err)
	}
}

func TestPlainSmallFileIsOneDirectBlob(t *testing.T) {
	e := setup(t, nil)
	data := make([]byte, 6*PartSize) // larger than a block of the test layout, still direct
	n := put(t, e, "movie.mp4", data, false)
	if err := e.upload(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	blocks, _ := e.Store.Blocks(n.ID)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks", len(blocks))
	}
}

func TestEncryptedBlobIsNotPlaintext(t *testing.T) {
	e := setup(t, nil)
	marker := bytes.Repeat([]byte("TOP-SECRET-MARKER"), 1000)
	n := put(t, e, "secret.txt", marker, true)
	if err := e.upload(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	backend := e.Backend.(*LocalBackend)
	entries, _ := os.ReadDir(backend.Dir)
	if len(entries) == 0 {
		t.Fatal("no blob")
	}
	for _, entry := range entries {
		raw, _ := os.ReadFile(filepath.Join(backend.Dir, entry.Name()))
		if bytes.Contains(raw, []byte("TOP-SECRET")) {
			t.Fatal("plaintext reached the backend")
		}
	}
}

func TestTamperedBlobIsRejected(t *testing.T) {
	e := setup(t, nil)
	n := put(t, e, "x", make([]byte, 2000), true)
	if err := e.upload(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	e.Store.MarkReady(n.ID)
	n, _ = e.Store.Get(n.ID)
	blocks, _ := e.Store.Blocks(n.ID)
	path := e.Backend.(*LocalBackend).path(blocks[0].MsgID)
	raw, _ := os.ReadFile(path)
	raw[100] ^= 1
	os.WriteFile(path, raw, 0o600)
	if err := e.Verify(context.Background(), n); err == nil {
		t.Fatal("manipulation was not detected")
	}
}

func TestUploadResumesAfterFailure(t *testing.T) {
	dir := t.TempDir()
	local, _ := NewLocalBackend(dir)
	flaky := &flakyBackend{LocalBackend: local, fails: 2}
	e := setup(t, flaky)
	data := make([]byte, 9*PartSize)
	rand.Read(data)
	n := put(t, e, "big", data, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Fast retries: the worker's backoff is exercised by resetting next_try.
	e.Start(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur, _ := e.Store.Get(n.ID)
		if cur.State == store.StateReady {
			n = cur
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready: %+v", cur)
		}
		e.Store.RetryNow(n.ID)
		e.Wake()
		time.Sleep(20 * time.Millisecond)
	}
	if err := e.Verify(ctx, n); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeQueuesMessages(t *testing.T) {
	e := setup(t, nil)
	n := put(t, e, "gone", make([]byte, 100), true)
	if err := e.upload(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	e.Delete(n.ID)
	if err := e.Purge(n.ID); err != nil {
		t.Fatal(err)
	}
	e.janitor(context.Background())
	entries, _ := os.ReadDir(e.Backend.(*LocalBackend).Dir)
	if len(entries) != 0 {
		t.Fatalf("%d blobs left", len(entries))
	}
}

func TestBackupRestore(t *testing.T) {
	e := setup(t, nil)
	v, header, err := vault.Create("geheim")
	if err != nil {
		t.Fatal(err)
	}
	e.SetVault(v)
	e.Store.SetSetting("vault", header)
	put(t, e, "keep.txt", []byte("x"), true)
	if err := e.Backup(context.Background()); err != nil {
		t.Fatal(err)
	}
	dir := e.Backend.(*LocalBackend).Dir
	entries, _ := os.ReadDir(dir)
	raw, _ := os.ReadFile(filepath.Join(dir, entries[len(entries)-1].Name()))
	if _, err := vault.OpenBackup(raw, "falsch"); !errors.Is(err, vault.ErrWrongPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	db, err := vault.OpenBackup(raw, "geheim")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	os.WriteFile(path, db, 0o600)
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Child(store.RootID, "keep.txt"); err != nil {
		t.Fatal(err)
	}
}
