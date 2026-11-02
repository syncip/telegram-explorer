package files

import (
	"context"
	"testing"
	"time"
)

func TestThrottle(t *testing.T) {
	var th Throttle
	ctx := context.Background()
	start := time.Now()
	for range 3 {
		th.Wait(ctx, 1<<20)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("no limit must not wait")
	}

	th.SetRate(10 << 20)
	start = time.Now()
	for range 3 {
		th.Wait(ctx, 1<<20)
	}
	if d := time.Since(start); d < 180*time.Millisecond || d > 600*time.Millisecond {
		t.Fatalf("3 MiB at 10 MiB/s took %s", d)
	}

	// Lifting the limit frees whoever waits.
	th.SetRate(1 << 10)
	th.Wait(ctx, 1<<20)
	done := make(chan struct{})
	go func() {
		th.Wait(ctx, 1<<20)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	th.SetRate(0)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiting transfer kept the old limit")
	}
}

func TestMeter(t *testing.T) {
	var m Meter
	now := time.Now()
	m.tick(now)
	m.Add(3 << 20)
	m.tick(now.Add(time.Second))
	m.Add(1 << 20)
	m.tick(now.Add(2 * time.Second))
	if got := m.Rate(); got != 2<<20 {
		t.Fatalf("rate %d", got)
	}
}
