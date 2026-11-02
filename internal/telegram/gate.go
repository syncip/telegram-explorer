package telegram

import (
	"context"
	"sync"
	"time"
)

const (
	// minGap and maxGap bound the spacing between reads once Telegram asked
	// for waits. Below minGap the spacing is dropped again.
	minGap = 50 * time.Millisecond
	maxGap = 2 * time.Second
)

// readGate limits how many file parts are read at once and how fast they
// start. Telegram answers too many with waits, so the gate halves and spaces
// the reads out on a wait, and opens up again after a run of reads without
// one: first the spacing goes, then the number of reads grows back.
type readGate struct {
	mu     sync.Mutex
	limit  int
	max    int
	active int
	quiet  int
	gap    time.Duration // least time between two starts, 0 while all is calm
	next   time.Time     // earliest start of the next read
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
			start := time.Now()
			if g.next.After(start) {
				start = g.next
			}
			g.next = start.Add(g.gap)
			g.mu.Unlock()
			return g.waitUntil(ctx, start)
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

// waitUntil holds a taken slot until its start time.
func (g *readGate) waitUntil(ctx context.Context, start time.Time) error {
	d := time.Until(start)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		g.mu.Lock()
		g.active--
		g.signal()
		g.mu.Unlock()
		return ctx.Err()
	}
}

func (g *readGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.quiet++; g.quiet >= 4*g.limit {
		g.quiet = 0
		switch {
		case g.gap > 0:
			if g.gap = g.gap * 3 / 4; g.gap < minGap {
				g.gap = 0
			}
		case g.limit < g.max:
			g.limit++
		}
	}
	g.signal()
}

// signal wakes waiting reads. g.mu must be held.
func (g *readGate) signal() {
	close(g.free)
	g.free = make(chan struct{})
}

func (g *readGate) shrink() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limit = max(1, g.limit/2)
	g.gap = min(maxGap, max(minGap, g.gap*2))
	g.quiet = 0
}
