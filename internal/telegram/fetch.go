package telegram

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"telegram-explorer/internal/files"
)

// fetcher reads file parts through one Telegram login: the account itself or a
// helper bot. Telegram allows each login about five parts a second, and each
// login sees its own file references, so every fetcher has its own connections,
// cache and gate.
type fetcher struct {
	c    *Client
	gate *readGate

	mu      sync.RWMutex
	name    string
	tg      *telegram.Client
	api     *tg.Client
	channel *tg.InputChannel

	docs sync.Map // msg id -> docRef

	poolMu sync.Mutex
	pools  map[int]*tg.Client // data center -> file connections

	parts  atomic.Int64 // since the last download figures
	floods atomic.Int64
}

func (c *Client) newFetcher(name string) *fetcher {
	slots := readSlots
	if c.Reads > 0 {
		slots = c.Reads
	}
	return &fetcher{c: c, name: name, gate: newReadGate(slots, c.wake)}
}

// attach hands the fetcher a fresh connection. Pools and file references of the
// old one are dropped.
func (f *fetcher) attach(client *telegram.Client) {
	f.mu.Lock()
	f.tg, f.api = client, client.API()
	f.mu.Unlock()
	f.poolMu.Lock()
	f.pools = map[int]*tg.Client{}
	f.poolMu.Unlock()
	f.docs.Clear()
}

// detach takes the fetcher out of service until the next attach.
func (f *fetcher) detach() {
	f.mu.Lock()
	f.tg, f.api, f.channel = nil, nil, nil
	f.mu.Unlock()
}

func (f *fetcher) setChannel(ch *tg.InputChannel) {
	f.mu.Lock()
	f.channel = ch
	f.mu.Unlock()
	f.c.wake()
}

func (f *fetcher) setName(name string) {
	f.mu.Lock()
	f.name = name
	f.mu.Unlock()
}

func (f *fetcher) label() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.name
}

func (f *fetcher) ready() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.api != nil && f.channel != nil
}

func (f *fetcher) conn() (*telegram.Client, *tg.Client, *tg.InputChannel) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.tg, f.api, f.channel
}

func (f *fetcher) remember(msgID int, doc *tg.Document) docRef {
	ref := docRef{
		location: &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference},
		dc:       doc.DCID,
		fetched:  time.Now(),
	}
	f.docs.Store(msgID, ref)
	return ref
}

// location returns a document location. File references expire, so they are
// refreshed from the message when old or when Telegram rejects them.
func (f *fetcher) location(ctx context.Context, msgID int, fresh bool) (docRef, error) {
	if v, ok := f.docs.Load(msgID); ok && !fresh {
		if ref := v.(docRef); time.Since(ref.fetched) < docCacheTTL {
			return ref, nil
		}
	}
	_, api, ch := f.conn()
	if api == nil || ch == nil {
		return docRef{}, files.ErrNotReady
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: ch,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
	})
	if err != nil {
		return docRef{}, err
	}
	list, ok := res.(*tg.MessagesChannelMessages)
	if !ok {
		return docRef{}, errors.New("unerwartete Antwort von Telegram")
	}
	for _, m := range list.Messages {
		if msg, ok := m.(*tg.Message); ok && msg.ID == msgID {
			doc, err := documentOf(msg)
			if err != nil {
				return docRef{}, err
			}
			return f.remember(msgID, doc), nil
		}
	}
	return docRef{}, fmt.Errorf("Nachricht %d: %w", msgID, files.ErrMissing)
}

// filePool returns the file connections to a data center. They bypass the
// client's middlewares, so these are applied here.
func (f *fetcher) filePool(ctx context.Context, dc int) (*tg.Client, error) {
	client, _, _ := f.conn()
	if client == nil {
		return nil, files.ErrNotReady
	}
	f.poolMu.Lock()
	defer f.poolMu.Unlock()
	if pool, ok := f.pools[dc]; ok {
		return pool, nil
	}
	var (
		conns telegram.CloseInvoker
		err   error
	)
	n := f.c.conns()
	if dc == client.Config().ThisDC {
		conns, err = client.Pool(int64(n))
	} else {
		conns, err = client.DC(ctx, dc, int64(n))
	}
	if err != nil {
		return nil, err
	}
	where := fmt.Sprintf("Dateiverbindungen DC %d von %s", dc, f.label())
	pool := tg.NewClient(floodWait{where: where, onWait: f.flooded, pass: isFileRead}.Handle(f.c.transferRetry().Handle(conns)))
	f.pools[dc] = pool
	log.Printf("telegram: %d Dateiverbindungen zu Rechenzentrum %d für %s aufgebaut", n, dc, f.label())
	return pool, nil
}

// flooded notes a rate limit on this login.
func (f *fetcher) flooded(input bin.Encoder, what string) {
	if isFileRead(input) {
		f.gate.shrink()
		f.floods.Add(1)
	}
	f.c.noteFlood(what)
}

// read fetches one part on a slot already taken from f.gate and frees the slot.
// When Telegram asks to wait, it returns how long instead of waiting, so the
// slot can serve another part meanwhile.
func (f *fetcher) read(ctx context.Context, msgID int, part int64) ([]byte, time.Duration, error) {
	fresh, refreshed := false, false
	for {
		ref, err := f.location(ctx, msgID, fresh)
		if err != nil {
			f.gate.release(false)
			return nil, 0, err
		}
		fresh = false
		_, api, _ := f.conn()
		if api == nil {
			f.gate.release(false)
			return nil, 0, files.ErrNotReady
		}
		reader := api
		if pool, err := f.filePool(ctx, ref.dc); err == nil {
			reader = pool
		} else {
			log.Printf("telegram: Dateiverbindungen zu Rechenzentrum %d für %s: %v", ref.dc, f.label(), err)
		}
		req := &tg.UploadGetFileRequest{Location: ref.location, Offset: part * files.PartSize, Limit: files.PartSize}
		res, err := reader.UploadGetFile(ctx, req)
		if tgerr.Is(err, "FILE_MIGRATE") {
			// The main connection knows how to follow a moved file.
			log.Printf("telegram: Nachricht %d liegt nicht in Rechenzentrum %d, %s liest über die Hauptverbindung", msgID, ref.dc, f.label())
			res, err = api.UploadGetFile(ctx, req)
		}
		if d, ok := tgerr.AsFloodWait(err); ok && d <= maxFloodWait {
			f.gate.release(false)
			return nil, max(d, 100*time.Millisecond), nil
		}
		if tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID") && !refreshed {
			fresh, refreshed = true, true
			continue
		}
		f.gate.release(err == nil)
		if err != nil {
			return nil, 0, err
		}
		file, ok := res.(*tg.UploadFile)
		if !ok {
			return nil, 0, fmt.Errorf("unerwartete Antwort von Telegram: %T", res)
		}
		f.parts.Add(1)
		return file.Bytes, 0, nil
	}
}

// account is the fetcher of the account's own login.
func (c *Client) account() *fetcher {
	c.acctOnce.Do(func() { c.acct = c.newFetcher("Konto") })
	return c.acct
}

// allFetchers lists every login, ready or not.
func (c *Client) allFetchers() []*fetcher {
	list := []*fetcher{c.account()}
	c.botsMu.Lock()
	for _, b := range c.bots {
		list = append(list, b.f)
	}
	c.botsMu.Unlock()
	return list
}

// fetchers lists the logins that can read right now, the account first.
func (c *Client) fetchers() []*fetcher {
	var list []*fetcher
	if c.Ready() && c.account().ready() {
		list = append(list, c.account())
	}
	c.botsMu.Lock()
	for _, b := range c.bots {
		if b.f.ready() {
			list = append(list, b.f)
		}
	}
	c.botsMu.Unlock()
	return list
}

// wake tells waiting reads that a slot or a login became free.
func (c *Client) wake() {
	c.wakeMu.Lock()
	if c.wakeCh != nil {
		close(c.wakeCh)
	}
	c.wakeCh = make(chan struct{})
	c.wakeMu.Unlock()
}

func (c *Client) wakeup() <-chan struct{} {
	c.wakeMu.Lock()
	defer c.wakeMu.Unlock()
	if c.wakeCh == nil {
		c.wakeCh = make(chan struct{})
	}
	return c.wakeCh
}

// pick takes a read slot from one of the logins, taking turns so all of them
// share the work.
func (c *Client) pick(ctx context.Context) (*fetcher, error) {
	for {
		wake := c.wakeup()
		list := c.fetchers()
		if len(list) == 0 {
			return nil, files.ErrNotReady
		}
		start := int(c.turn.Add(1))
		for i := range list {
			if f := list[(start+i)%len(list)]; f.gate.tryAcquire() {
				return f, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-wake:
		}
	}
}

// ReadPart implements files.Backend.
func (c *Client) ReadPart(ctx context.Context, msgID int, part int64) ([]byte, error) {
	if _, _, err := c.ready(); err != nil {
		return nil, err
	}
	for {
		f, err := c.pick(ctx)
		if err != nil {
			return nil, err
		}
		data, wait, err := f.read(ctx, msgID, part)
		switch {
		case wait > 0:
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		case err != nil && f != c.account() && ctx.Err() == nil && !errors.Is(err, files.ErrMissing):
			// A helper that fails is taken out; the part goes to another login.
			c.helperFailed(f, err)
			continue
		case err != nil:
			return nil, err
		}
		c.countRead(len(data))
		return data, nil
	}
}

// countRead adds a file part to the download figures, which go to the log
// once a minute while files are read.
func (c *Client) countRead(n int) {
	c.statsMu.Lock()
	defer c.statsMu.Unlock()
	now := time.Now()
	if c.statsStart.IsZero() {
		c.statsStart = now
	}
	c.statsParts++
	c.statsBytes += int64(n)
	took := now.Sub(c.statsStart)
	if took < time.Minute {
		return
	}
	var each []string
	floods := int64(0)
	for _, f := range c.allFetchers() {
		p, w := f.parts.Swap(0), f.floods.Swap(0)
		floods += w
		if p > 0 || w > 0 {
			each = append(each, fmt.Sprintf("%s %d Teile/%d Bremsen, %d gleichzeitig", f.label(), p, w, f.gate.state()))
		}
	}
	log.Printf("telegram: Download der letzten %ds: %d Teile, %.1f MB/s, %d Bremsen, %d Verbindungen je Abrufer (%s)",
		int(took.Seconds()), c.statsParts, float64(c.statsBytes)/took.Seconds()/1e6, floods, c.conns(), strings.Join(each, "; "))
	c.statsStart, c.statsParts, c.statsBytes = time.Time{}, 0, 0
}
