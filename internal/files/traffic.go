package files

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	limitUpSetting   = "limit_up"
	limitDownSetting = "limit_down"
	meterWindow      = 3 // seconds the shown speed averages over
)

// Throttle spreads transfers so they stay below a rate in bytes per second.
// Zero means no limit.
type Throttle struct {
	mu      sync.Mutex
	rate    int64
	next    time.Time
	changed chan struct{}
}

// SetRate changes the limit. Transfers already waiting start over with it.
func (t *Throttle) SetRate(rate int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rate = max(rate, 0)
	t.next = time.Time{}
	if t.changed != nil {
		close(t.changed)
		t.changed = nil
	}
}

// Rate returns the limit.
func (t *Throttle) Rate() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rate
}

// Wait blocks until n more bytes fit into the rate.
func (t *Throttle) Wait(ctx context.Context, n int) error {
	for {
		t.mu.Lock()
		if t.rate <= 0 {
			t.mu.Unlock()
			return nil
		}
		now := time.Now()
		if t.next.Before(now) {
			t.next = now
		}
		wait := t.next.Sub(now)
		t.next = t.next.Add(time.Duration(float64(n) / float64(t.rate) * float64(time.Second)))
		if t.changed == nil {
			t.changed = make(chan struct{})
		}
		changed := t.changed
		t.mu.Unlock()
		if wait <= 0 {
			return nil
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-changed:
			timer.Stop()
		}
	}
}

// Meter measures throughput.
type Meter struct {
	total   atomic.Int64
	mu      sync.Mutex
	samples []meterSample
}

type meterSample struct {
	at    time.Time
	total int64
}

// Add counts transferred bytes.
func (m *Meter) Add(n int) { m.total.Add(int64(n)) }

// tick takes a sample; it runs once a second.
func (m *Meter) tick(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, meterSample{now, m.total.Load()})
	if len(m.samples) > meterWindow+1 {
		m.samples = m.samples[1:]
	}
}

// Rate returns bytes per second over the last few seconds.
func (m *Meter) Rate() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.samples) < 2 {
		return 0
	}
	first, last := m.samples[0], m.samples[len(m.samples)-1]
	secs := last.at.Sub(first.at).Seconds()
	if secs <= 0 {
		return 0
	}
	return int64(float64(last.total-first.total) / secs)
}

// Speed is the current traffic to and from the backend.
type Speed struct {
	Up        int64 `json:"up"`
	Down      int64 `json:"down"`
	LimitUp   int64 `json:"limitUp"`
	LimitDown int64 `json:"limitDown"`
}

// Speed returns the current rates and limits.
func (e *Engine) Speed() Speed {
	return Speed{Up: e.upMeter.Rate(), Down: e.downMeter.Rate(), LimitUp: e.up.Rate(), LimitDown: e.down.Rate()}
}

// SetLimits stores and applies the limits in bytes per second, zero for none.
func (e *Engine) SetLimits(up, down int64) error {
	if err := e.Store.SetSetting(limitUpSetting, []byte(strconv.FormatInt(up, 10))); err != nil {
		return err
	}
	if err := e.Store.SetSetting(limitDownSetting, []byte(strconv.FormatInt(down, 10))); err != nil {
		return err
	}
	e.up.SetRate(up)
	e.down.SetRate(down)
	return nil
}

func (e *Engine) loadLimits() {
	for key, t := range map[string]*Throttle{limitUpSetting: &e.up, limitDownSetting: &e.down} {
		raw, _ := e.Store.Setting(key)
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			t.SetRate(n)
		}
	}
}

func (e *Engine) meterLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			e.upMeter.tick(now)
			e.downMeter.tick(now)
		}
	}
}

// pacedReader feeds an upload through the upload limit.
type pacedReader struct {
	ctx context.Context
	r   interface{ Read([]byte) (int, error) }
	e   *Engine
}

func (p *pacedReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.e.upMeter.Add(n)
		if werr := p.e.up.Wait(p.ctx, n); werr != nil {
			return n, werr
		}
	}
	return n, err
}
