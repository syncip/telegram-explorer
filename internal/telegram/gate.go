package telegram

import (
	"sync"
	"time"
)

// shrinkHold keeps the waits Telegram hands out for one burst from halving the
// window more than once.
const shrinkHold = time.Second

// readGate limits how many file parts are read at once. Telegram takes a
// varying number, so the gate learns it like a TCP congestion window: it
// halves on a wait and grows by one for every window of answered reads. A wait
// hits only its own part; the others go on. Every login has its own gate.
type readGate struct {
	mu       sync.Mutex
	limit    int
	max      int
	active   int
	answered int
	shrunk   time.Time
	onFree   func() // runs when a slot frees up
}

func newReadGate(max int, onFree func()) *readGate {
	return &readGate{limit: max, max: max, onFree: onFree}
}

// tryAcquire takes a slot if one is free.
func (g *readGate) tryAcquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= g.limit {
		return false
	}
	g.active++
	return true
}

// release frees a slot. answered says whether Telegram answered the read
// rather than asking to wait.
func (g *readGate) release(answered bool) {
	g.mu.Lock()
	g.active--
	if answered {
		if g.answered++; g.answered >= g.limit && g.limit < g.max {
			g.limit++
			g.answered = 0
		}
	}
	g.mu.Unlock()
	if g.onFree != nil {
		g.onFree()
	}
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
