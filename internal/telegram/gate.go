package telegram

import (
	"context"
	"sync"
	"time"
)

// shrinkHold keeps the waits Telegram hands out for one burst from halving the
// window more than once.
const shrinkHold = time.Second

// readGate limits how many file parts are read at once. Telegram takes a
// varying number, so the gate learns it like a TCP congestion window: it
// halves on a wait and grows by one for every window of answered reads. A wait
// hits only its own part; the others go on.
type readGate struct {
	mu       sync.Mutex
	limit    int
	max      int
	active   int
	answered int
	shrunk   time.Time
	free     chan struct{} // closed when a slot frees up
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

// release frees a slot. answered says whether Telegram answered the read
// rather than asking to wait.
func (g *readGate) release(answered bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if answered {
		if g.answered++; g.answered >= g.limit && g.limit < g.max {
			g.limit++
			g.answered = 0
		}
	}
	close(g.free)
	g.free = make(chan struct{})
}

// state reports how many reads may run at once.
func (g *readGate) state() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limit
}

func (g *readGate) shrink() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Since(g.shrunk) < shrinkHold {
		return
	}
	g.shrunk = time.Now()
	g.limit = max(1, g.limit/2)
	g.answered = 0
}
