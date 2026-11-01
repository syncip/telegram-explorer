package files

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"telegram-explorer/internal/store"
	"telegram-explorer/internal/vault"
)

// ErrCorrupt means the stored data is not what was uploaded.
var ErrCorrupt = vault.ErrCorrupt

// VerifyStatus is the progress of the check queue.
type VerifyStatus struct {
	Running    bool     `json:"running"`
	Done       int      `json:"done"`
	Total      int      `json:"total"`
	BytesDone  int64    `json:"bytesDone"`
	BytesTotal int64    `json:"bytesTotal"`
	Current    string   `json:"current"`
	Bad        []string `json:"bad"`
	Failed     []string `json:"failed"`
}

type verifier struct {
	mu      sync.Mutex
	queue   []*store.Node
	queued  map[int64]bool
	status  VerifyStatus
	cancel  context.CancelFunc
	current atomic.Int64
}

// Verify downloads a whole file, compares it with the recorded checksums and
// stores the result. Only real damage counts as damage: a check that could not
// finish, say because Telegram was away, says nothing about the file.
func (e *Engine) Verify(ctx context.Context, n *store.Node) error {
	sum, err := e.check(ctx, n)
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case err == nil:
		e.Store.SetVerify(n.ID, store.VerifyOK, "", sum)
	case errors.Is(err, ErrCorrupt), errors.Is(err, ErrMissing), errors.Is(err, io.ErrUnexpectedEOF):
		e.Store.SetVerify(n.ID, store.VerifyBad, err.Error(), "")
		e.Store.Log(store.LevelError, store.KindVerify, "„%s“ ist beschädigt: %v", n.Name, err)
	default:
		e.Store.SetVerify(n.ID, store.VerifyError, err.Error(), "")
		e.Store.Log(store.LevelWarn, store.KindVerify, "„%s“ konnte nicht geprüft werden: %v", n.Name, err)
	}
	return err
}

func (e *Engine) check(ctx context.Context, n *store.Node) (string, error) {
	r, err := e.Open(ctx, n)
	if err != nil {
		return "", err
	}
	defer r.Close()
	s, m := sha256.New(), md5.New()
	counted := &countingWriter{n: &e.verifier.current}
	size, err := io.Copy(io.MultiWriter(s, m, counted), r)
	if err != nil {
		return "", err
	}
	if size != n.Size {
		return "", fmt.Errorf("%w: %d statt %d Bytes", ErrCorrupt, size, n.Size)
	}
	if n.SHA256 != "" && hex.EncodeToString(s.Sum(nil)) != n.SHA256 {
		return "", fmt.Errorf("%w: Prüfsumme stimmt nicht überein", ErrCorrupt)
	}
	if n.MD5 != "" && hex.EncodeToString(m.Sum(nil)) != n.MD5 {
		return "", fmt.Errorf("%w: Prüfsumme stimmt nicht überein", ErrCorrupt)
	}
	return hex.EncodeToString(m.Sum(nil)), nil
}

type countingWriter struct{ n *atomic.Int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return len(p), nil
}

// EnqueueVerify queues files, and everything below folders, for a check.
func (e *Engine) EnqueueVerify(ids []int64) (int, error) {
	var nodes []*store.Node
	for _, id := range ids {
		n, err := e.Store.Get(id)
		if err != nil {
			return 0, err
		}
		if !n.IsDir {
			nodes = append(nodes, n)
			continue
		}
		sub, err := e.Store.Descendants(id, "")
		if err != nil {
			return 0, err
		}
		for _, child := range sub {
			nodes = append(nodes, child)
		}
	}
	v := &e.verifier
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.status.Running {
		v.status = VerifyStatus{}
		v.queued = map[int64]bool{}
		v.current.Store(0)
	}
	added := 0
	for _, n := range nodes {
		if n.State != store.StateReady || v.queued[n.ID] {
			continue
		}
		v.queued[n.ID] = true
		v.queue = append(v.queue, n)
		v.status.Total++
		v.status.BytesTotal += n.Size
		added++
	}
	if added > 0 && !v.status.Running {
		ctx, cancel := context.WithCancel(e.ctx)
		v.cancel = cancel
		v.status.Running = true
		go e.verifyLoop(ctx)
	}
	return added, nil
}

func (e *Engine) verifyLoop(ctx context.Context) {
	v := &e.verifier
	for {
		v.mu.Lock()
		if len(v.queue) == 0 || ctx.Err() != nil {
			v.queue = nil
			v.status.Running = false
			v.status.Current = ""
			summary := v.status
			v.mu.Unlock()
			if ctx.Err() == nil {
				e.Store.Log(store.LevelInfo, store.KindVerify, "Prüfung beendet: %d geprüft, %d beschädigt, %d nicht prüfbar",
					summary.Done, len(summary.Bad), len(summary.Failed))
			}
			return
		}
		n := v.queue[0]
		v.queue = v.queue[1:]
		v.status.Current = n.Name
		v.mu.Unlock()

		err := e.Verify(ctx, n)

		v.mu.Lock()
		switch {
		case ctx.Err() != nil:
		case err == nil:
			v.status.Done++
		case errors.Is(err, ErrCorrupt), errors.Is(err, ErrMissing), errors.Is(err, io.ErrUnexpectedEOF):
			v.status.Done++
			v.status.Bad = append(v.status.Bad, n.Name)
		default:
			v.status.Done++
			v.status.Failed = append(v.status.Failed, n.Name)
		}
		v.mu.Unlock()
	}
}

// VerifyState returns the queue's progress.
func (e *Engine) VerifyState() VerifyStatus {
	v := &e.verifier
	v.mu.Lock()
	defer v.mu.Unlock()
	s := v.status
	s.BytesDone = v.current.Load()
	return s
}

// ClearVerify stops a running check and forgets the last result.
func (e *Engine) ClearVerify() {
	v := &e.verifier
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cancel != nil {
		v.cancel()
	}
	if !v.status.Running {
		v.status = VerifyStatus{}
	}
}
