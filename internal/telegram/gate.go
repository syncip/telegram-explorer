package telegram

import (
	"context"
	"sync"
)

// readGate limits how many file parts are read at once. Telegram answers too
// many with waits, so the gate halves on a wait and grows back by one after a
// run of reads without one.
type readGate struct {
	mu     sync.Mutex
	limit  int
	max    int
	active int
	quiet  int
	free   chan struct{} // closed when a slot frees up
}

func newReadGate(max int) *readGate {
	return &readGate{limit: max, max: max, free: make(chan struct{})}
}

func (g *readGate) acquire(ctx context.Context) error {
	for {
		g.mu.Lock()
		if g.active < g.limit {
			g.active++
			g.mu.Unlock()
			return nil
		}
		free := g.free
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-free:
		}
	}
}

func (g *readGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.quiet++; g.quiet >= 4*g.limit && g.limit < g.max {
		g.limit++
		g.quiet = 0
	}
	close(g.free)
	g.free = make(chan struct{})
}

func (g *readGate) shrink() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limit = max(1, g.limit/2)
	g.quiet = 0
}
