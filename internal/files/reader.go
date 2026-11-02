package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"telegram-explorer/internal/store"
	"telegram-explorer/internal/vault"
)

const (
	readAhead  = 8
	cacheParts = 16
)

// Reader reads a file with random access. It is safe for concurrent ReadAt
// calls, because SFTP clients issue overlapping requests.
type Reader struct {
	e      *Engine
	node   *store.Node
	blocks []store.Block
	cipher *vault.FileCipher
	layout layout
	stage  *os.File

	ctx    context.Context
	cancel context.CancelFunc

	mu    sync.Mutex
	cache map[int64]*fetch
	order []int64
	pos   int64
}

type fetch struct {
	done chan struct{}
	data []byte
	err  error
}

// Open returns a reader for a file. Files still waiting for their upload are
// served from the stage file.
func (e *Engine) Open(ctx context.Context, n *store.Node) (*Reader, error) {
	if n.IsDir {
		return nil, errors.New("ist ein Ordner")
	}
	ctx, cancel := context.WithCancel(ctx)
	r := &Reader{e: e, node: n, ctx: ctx, cancel: cancel, cache: map[int64]*fetch{}}
	if n.StagePath != "" {
		f, err := os.Open(n.StagePath)
		if err == nil {
			r.stage = f
			return r, nil
		}
		// The upload may have finished right now; the fresh record tells.
		fresh, gerr := e.Store.Get(n.ID)
		if gerr != nil || fresh.StagePath != "" {
			cancel()
			return nil, err
		}
		r.node, n = fresh, fresh
	}
	if n.Size == 0 {
		return r, nil
	}
	if n.Encrypted {
		v := e.Vault()
		if v == nil {
			cancel()
			return nil, ErrLocked
		}
		fc, err := v.FileCipher(n.WrappedKey, n.Size)
		if err != nil {
			cancel()
			return nil, err
		}
		r.cipher = fc
	}
	blocks, err := e.Store.Blocks(n.ID)
	if err != nil {
		cancel()
		return nil, err
	}
	r.blocks = blocks
	r.layout = e.layoutOf(n.Size, n.Encrypted)
	if len(blocks) == 0 {
		cancel()
		return nil, errors.New("Datei hat keine gespeicherten Blöcke")
	}
	// The layout written at upload time wins over the current configuration.
	r.layout.blockParts = ceilDiv(blocks[0].Size, PartSize)
	return r, nil
}

// Close releases the reader.
func (r *Reader) Close() error {
	r.cancel()
	if r.stage != nil {
		return r.stage.Close()
	}
	return nil
}

// Size returns the plaintext size.
func (r *Reader) Size() int64 { return r.node.Size }

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	r.mu.Lock()
	pos := r.pos
	r.mu.Unlock()
	n, err := r.ReadAt(p, pos)
	r.mu.Lock()
	r.pos = pos + int64(n)
	r.mu.Unlock()
	if n > 0 && errors.Is(err, io.EOF) {
		err = nil
	}
	return n, err
}

// Seek implements io.Seeker.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.pos
	case io.SeekEnd:
		offset += r.node.Size
	default:
		return 0, errors.New("invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	r.pos = offset
	return offset, nil
}

// ReadAt implements io.ReaderAt.
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if r.stage != nil {
		return r.stage.ReadAt(p, off)
	}
	if off >= r.node.Size {
		return 0, io.EOF
	}
	total := 0
	for len(p) > 0 && off < r.node.Size {
		seg := off / r.layout.segPlain
		data, err := r.segment(seg)
		if err != nil {
			return total, err
		}
		start := off - seg*r.layout.segPlain
		if start >= int64(len(data)) {
			return total, io.ErrUnexpectedEOF
		}
		n := copy(p, data[start:])
		p = p[n:]
		off += int64(n)
		total += n
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}

func (r *Reader) segment(seg int64) ([]byte, error) {
	r.mu.Lock()
	f := r.start(seg)
	segments := ceilDiv(r.node.Size, r.layout.segPlain)
	for i := seg + 1; i <= seg+readAhead && i < segments; i++ {
		r.start(i)
	}
	r.mu.Unlock()
	select {
	case <-f.done:
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
	if f.err != nil {
		r.mu.Lock()
		// A failed part must not stick; a later read may try again.
		if r.cache[seg] == f {
			delete(r.cache, seg)
		}
		r.mu.Unlock()
	}
	return f.data, f.err
}

// start begins fetching a segment unless it is cached. r.mu must be held.
func (r *Reader) start(seg int64) *fetch {
	if f, ok := r.cache[seg]; ok {
		return f
	}
	f := &fetch{done: make(chan struct{})}
	r.cache[seg] = f
	r.order = append(r.order, seg)
	for len(r.order) > cacheParts {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
	go func() {
		defer close(f.done)
		f.data, f.err = r.load(seg)
	}()
	return f
}

func (r *Reader) load(seg int64) ([]byte, error) {
	idx := seg / r.layout.blockParts
	if idx >= int64(len(r.blocks)) {
		return nil, fmt.Errorf("Block %d fehlt", idx)
	}
	if err := r.e.down.Wait(r.ctx, PartSize); err != nil {
		return nil, err
	}
	data, err := r.e.Backend.ReadPart(r.ctx, r.blocks[idx].MsgID, seg%r.layout.blockParts)
	if err != nil {
		return nil, err
	}
	r.e.downMeter.Add(len(data))
	if r.cipher == nil {
		return data, nil
	}
	return r.cipher.OpenSegment(seg, data)
}
