package telegram

import (
	"testing"
	"time"
)

func TestReadGate(t *testing.T) {
	freed := 0
	g := newReadGate(8, func() { freed++ })
	g.shrink()
	g.shrink() // the same burst
	if g.limit != 4 {
		t.Fatalf("limit %d after one burst of waits", g.limit)
	}
	for range 4 {
		if !g.tryAcquire() {
			t.Fatal("a halved gate must still take four reads")
		}
	}
	if g.tryAcquire() {
		t.Fatal("a halved gate must hold the fifth read")
	}

	// Waits free their slot without opening the gate.
	for range 4 {
		g.release(false)
	}
	if g.limit != 4 || freed != 4 {
		t.Fatalf("limit %d, %d wake-ups after waits", g.limit, freed)
	}

	// One window of answers adds one slot, never beyond the start.
	for range 4 {
		g.tryAcquire()
		g.release(true)
	}
	if g.limit != 5 {
		t.Fatalf("limit %d after one window of answers", g.limit)
	}
	for range 200 {
		g.tryAcquire()
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
