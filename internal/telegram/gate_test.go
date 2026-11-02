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
	g.gap = 0 // spacing has its own test
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
	if g.limit != 1 || g.gap != maxGap {
		t.Fatalf("limit %d, gap %v after many waits", g.limit, g.gap)
	}
}

func TestReadGateSpacing(t *testing.T) {
	g := newReadGate(8)
	ctx := context.Background()
	g.shrink()
	g.limit = 8
	begin := time.Now()
	for range 3 {
		if err := g.acquire(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(begin); took < 2*minGap {
		t.Fatalf("three reads started within %v despite a gap of %v", took, minGap)
	}

	// A cancelled wait gives its slot back.
	g.next = time.Now().Add(time.Hour)
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := g.acquire(short); err == nil {
		t.Fatal("a read an hour away must not start")
	}
	if g.active != 3 {
		t.Fatalf("%d active reads, want 3", g.active)
	}

	// Calm reads drop the spacing before anything else.
	for range 3 {
		g.release()
	}
	g.next = time.Time{}
	for range 40 {
		g.acquire(ctx)
		g.release()
	}
	if g.gap != 0 {
		t.Fatalf("gap %v after calm reads", g.gap)
	}
}
