package telegram

import (
	"context"
	"testing"
	"time"
)

func TestReadGate(t *testing.T) {
	g := newReadGate(8)
	ctx := context.Background()
	g.shrink()
	g.shrink() // the same burst
	if g.limit != 4 {
		t.Fatalf("limit %d after one burst of waits", g.limit)
	}
	for range 4 {
		if err := g.acquire(ctx); err != nil {
			t.Fatal(err)
		}
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := g.acquire(short); err == nil {
		t.Fatal("a halved gate must hold the fifth read")
	}

	// Waits free their slot without opening the gate.
	for range 4 {
		g.release(false)
	}
	if g.limit != 4 {
		t.Fatalf("limit %d after waits", g.limit)
	}

	// One window of answers adds one slot, never beyond the start.
	for range 4 {
		g.acquire(ctx)
		g.release(true)
	}
	if g.limit != 5 {
		t.Fatalf("limit %d after one window of answers", g.limit)
	}
	for range 200 {
		g.acquire(ctx)
		g.release(true)
	}
	if g.limit != 8 {
		t.Fatalf("limit %d after many answers", g.limit)
	}

	for range 10 {
		g.shrunk = time.Time{}
		g.shrink()
	}
	if g.limit != 1 {
		t.Fatalf("limit %d after many bursts", g.limit)
	}
}
