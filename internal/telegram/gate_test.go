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
	for range 4 {
		g.release()
	}

	// Quiet reads open it up again, never beyond the start.
	for range 200 {
		g.acquire(ctx)
		g.release()
	}
	if g.limit != 8 {
		t.Fatalf("limit %d after quiet reads", g.limit)
	}
	for range 10 {
		g.shrink()
	}
	if g.limit != 1 {
		t.Fatalf("limit %d after many waits", g.limit)
	}
}
