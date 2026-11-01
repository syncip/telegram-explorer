package files

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// LocalBackend keeps blobs in a directory. It stands in for Telegram during
// development and in tests.
type LocalBackend struct {
	Dir string

	mu   sync.Mutex
	next int
}

// NewLocalBackend prepares the directory.
func NewLocalBackend(dir string) (*LocalBackend, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	b := &LocalBackend{Dir: dir}
	for _, entry := range entries {
		var id int
		if _, err := fmt.Sscanf(entry.Name(), "%d", &id); err == nil && id > b.next {
			b.next = id
		}
	}
	return b, nil
}

func (b *LocalBackend) path(id int) string { return filepath.Join(b.Dir, fmt.Sprintf("%d.blob", id)) }

// Ready implements Backend.
func (b *LocalBackend) Ready() bool { return true }

// Put implements Backend.
func (b *LocalBackend) Put(_ context.Context, _, _ string, r io.Reader, size int64, _ func(int64)) (int, error) {
	b.mu.Lock()
	b.next++
	id := b.next
	b.mu.Unlock()
	f, err := os.Create(b.path(id))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		return 0, err
	}
	if n != size {
		return 0, fmt.Errorf("Größe %d statt %d", n, size)
	}
	return id, nil
}

// ReadPart implements Backend.
func (b *LocalBackend) ReadPart(_ context.Context, id int, part int64) ([]byte, error) {
	f, err := os.Open(b.path(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, PartSize)
	n, err := f.ReadAt(buf, part*PartSize)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// Delete implements Backend.
func (b *LocalBackend) Delete(_ context.Context, ids []int) error {
	for _, id := range ids {
		if err := os.Remove(b.path(id)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
