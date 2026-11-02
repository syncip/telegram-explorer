// Package telegram stores blobs as documents in a private channel of the user's
// own account, using MTProto.
package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
	"telegram-explorer/internal/vault"
)

const (
	channelTitle  = "Telegram Explorer Storage"
	uploadPart    = 512 * 1024
	uploadThreads = 4
	maxFloodWait  = 10 * time.Minute
	docCacheTTL   = 30 * time.Minute
	sizeBatch     = 100
	// transferConns is the default number of connections per data center for
	// reading files. Telegram allows an account about five file parts a second,
	// however many connections ask, and one connection already gets there.
	transferConns = 1

	transferRetries = 8
	transferDelay   = time.Second

	// readSlots is the default for the most file parts read at once.
	readSlots = 4
)

// Login steps.
const (
	StepConnecting = "connecting"
	StepPhone      = "phone"
	StepCode       = "code"
	StepPassword   = "password"
	StepReady      = "ready"
)

// Status is the connection state shown in the UI.
type Status struct {
	Step  string `json:"step"`
	User  string `json:"user,omitempty"`
	Error string `json:"error,omitempty"`
}

type channelRef struct {
	ID         int64 `json:"id"`
	AccessHash int64 `json:"accessHash"`
}

type docRef struct {
	location *tg.InputDocumentFileLocation
	dc       int
	fetched  time.Time
}

// Client implements files.Backend.
type Client struct {
	AppID   int
	AppHash string
	Store   *store.Store
	Vault   func() *vault.Vault
	// Conns and Reads override transferConns and readSlots, for finding out
	// what Telegram allows an account.
	Conns int
	Reads int

	mu       sync.RWMutex
	status   Status
	api      *tg.Client
	auth     *auth.Client
	tg       *telegram.Client
	channel  *channelRef
	phone    string
	codeHash string

	// BotTokens are helper bots from the environment. More are added in the
	// web interface.
	BotTokens []string

	acct     *fetcher // file reads through the account
	acctOnce sync.Once
	botsMu   sync.Mutex
	bots     []*bot
	botCtx   context.Context
	createMu sync.Mutex

	wakeMu sync.Mutex
	wakeCh chan struct{}
	turn   atomic.Uint64

	floodMu     sync.Mutex
	floodCounts map[string]int
	floodLogged time.Time

	statsMu    sync.Mutex
	statsStart time.Time
	statsParts int
	statsBytes int64
}

func (c *Client) conns() int {
	if c.Conns > 0 {
		return c.Conns
	}
	return transferConns
}

// sessionStorage keeps the MTProto session sealed: it grants full account access.
type sessionStorage struct{ c *Client }

func (s sessionStorage) LoadSession(context.Context) ([]byte, error) {
	sealed, err := s.c.Store.Setting("tg_session")
	if err != nil {
		return nil, err
	}
	if sealed == nil {
		return nil, session.ErrNotFound
	}
	return s.c.Vault().Open("tg-session", sealed)
}

func (s sessionStorage) StoreSession(_ context.Context, data []byte) error {
	sealed, err := s.c.Vault().Seal("tg-session", data)
	if err != nil {
		return err
	}
	return s.c.Store.SetSetting("tg_session", sealed)
}

// floodWait sleeps through server rate limits instead of failing the request.
// where names the connection in the log. Requests for which pass is true get
// the error back instead, because their caller waits without holding a slot.
type floodWait struct {
	where  string
	onWait func(input bin.Encoder, what string)
	pass   func(input bin.Encoder) bool
}

func isFileRead(input bin.Encoder) bool {
	_, ok := input.(*tg.UploadGetFileRequest)
	return ok
}

// method names a request the way Telegram documents it, like upload.getFile.
func method(input bin.Encoder) string {
	if t, ok := input.(interface{ TypeName() string }); ok {
		return t.TypeName()
	}
	return fmt.Sprintf("%T", input)
}

func (f floodWait) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		for {
			err := next.Invoke(ctx, input, output)
			d, ok := tgerr.AsFloodWait(err)
			if !ok || d > maxFloodWait {
				return err
			}
			kind := "FLOOD_WAIT"
			if e, ok := tgerr.As(err); ok {
				kind = e.Type
			}
			what := fmt.Sprintf("%s bei %s über %s", kind, method(input), f.where)
			if f.onWait != nil {
				f.onWait(input, what)
			}
			log.Printf("telegram: Limit erreicht (%s), warte %s", what, d)
			if f.pass != nil && f.pass(input) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(max(d, 100*time.Millisecond)):
			}
		}
	}
}

// transferRetry repeats file parts after a lost connection. The library leaves
// acknowledged requests alone, because Telegram may have run them already. For
// file parts that is harmless, and without this one dropped connection would
// throw away everything sent for the block so far.
type transferRetry struct {
	notify func(error)
	delay  time.Duration
}

func (t transferRetry) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		for attempt := 0; ; attempt++ {
			err := next.Invoke(ctx, input, output)
			if err == nil || attempt == transferRetries || ctx.Err() != nil || !repeatable(input) {
				return err
			}
			if _, answered := tgerr.As(err); answered {
				return err
			}
			if attempt == 0 {
				t.notify(err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(min(t.delay<<attempt, 30*t.delay)):
			}
		}
	}
}

func repeatable(input bin.Encoder) bool {
	switch input.(type) {
	case *tg.UploadSaveBigFilePartRequest, *tg.UploadSaveFilePartRequest, *tg.UploadGetFileRequest:
		return true
	}
	return false
}

func (c *Client) transferRetry() transferRetry {
	return transferRetry{delay: transferDelay, notify: func(err error) {
		c.Store.Log(store.LevelWarn, store.KindTelegram, "Verbindung unterbrochen, Dateiteil wird wiederholt: %v", err)
	}}
}

// noteFlood counts a rate limit for the activity log, which says once a
// minute which limit hit which request, because a premium account should not
// see FLOOD_PREMIUM_WAIT.
func (c *Client) noteFlood(what string) {
	c.floodMu.Lock()
	defer c.floodMu.Unlock()
	if c.floodCounts == nil {
		c.floodCounts = map[string]int{}
	}
	c.floodCounts[what]++
	if time.Since(c.floodLogged) < time.Minute {
		return
	}
	var parts []string
	for k, n := range c.floodCounts {
		parts = append(parts, fmt.Sprintf("%s %d×", k, n))
	}
	sort.Strings(parts)
	note := ""
	for k := range c.floodCounts {
		if strings.HasPrefix(k, "FLOOD_PREMIUM_WAIT") {
			note = ". FLOOD_PREMIUM_WAIT ist die Bremse für Konten ohne Premium"
		}
	}
	c.Store.Log(store.LevelWarn, store.KindTelegram, "Telegram bremst die Übertragung: %s%s", strings.Join(parts, ", "), note)
	c.floodCounts = map[string]int{}
	c.floodLogged = time.Now()
}

func (c *Client) setStatus(s Status) {
	c.mu.Lock()
	c.status = s
	c.mu.Unlock()
}

// Status returns the current connection state.
func (c *Client) Status() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// Ready implements files.Backend.
func (c *Client) Ready() bool { return c.Status().Step == StepReady }

// Run keeps the connection alive until ctx ends. The vault must be unlocked.
func (c *Client) Run(ctx context.Context) {
	c.startBots(ctx)
	delay := time.Second
	for ctx.Err() == nil {
		c.setStatus(Status{Step: StepConnecting})
		started := time.Now()
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		c.Store.Log(store.LevelWarn, store.KindTelegram, "Verbindung getrennt: %v", err)
		c.setStatus(Status{Step: StepConnecting, Error: fmt.Sprint(err)})
		if time.Since(started) > time.Minute {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 2*time.Minute)
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	client := telegram.NewClient(c.AppID, c.AppHash, telegram.Options{
		SessionStorage: sessionStorage{c},
		Middlewares:    []telegram.Middleware{floodWait{where: "Hauptverbindung", onWait: c.account().flooded}, c.transferRetry()},
		Device:         telegram.DeviceConfig{DeviceModel: "Telegram Explorer", SystemVersion: "Docker", AppVersion: "1.0"},
	})
	return client.Run(ctx, func(ctx context.Context) error {
		c.mu.Lock()
		c.api, c.auth, c.tg = client.API(), client.Auth(), client
		c.mu.Unlock()
		c.account().attach(client)
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status: %w", err)
		}
		if status.Authorized {
			if err := c.finishLogin(ctx, status.User); err != nil {
				return err
			}
		} else if step := c.Status().Step; step != StepCode && step != StepPassword {
			c.setStatus(Status{Step: StepPhone})
		}
		<-ctx.Done()
		return ctx.Err()
	})
}

func (c *Client) finishLogin(ctx context.Context, user *tg.User) error {
	if err := c.ensureChannel(ctx); err != nil {
		return fmt.Errorf("Speicherkanal: %w", err)
	}
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if user.Username != "" {
		name += " (@" + user.Username + ")"
	}
	c.setStatus(Status{Step: StepReady, User: name})
	premium := "ohne Premium"
	if user.Premium {
		premium = "mit Premium"
	}
	c.Store.Log(store.LevelInfo, store.KindTelegram, "Verbunden als %s, Konto %s", name, premium)
	log.Printf("telegram: verbunden, Konto %s", premium)
	return nil
}

func (c *Client) ensureChannel(ctx context.Context) error {
	raw, err := c.Store.Setting("tg_channel")
	if err != nil {
		return err
	}
	if raw != nil {
		var ref channelRef
		if err := json.Unmarshal(raw, &ref); err != nil {
			return err
		}
		c.useChannel(ref)
		return nil
	}
	res, err := c.client().ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Broadcast: true,
		Title:     channelTitle,
		About:     "Von Telegram Explorer verwaltet. Nachrichten hier nicht löschen.",
	})
	if err != nil {
		return err
	}
	var chats []tg.ChatClass
	switch u := res.(type) {
	case *tg.Updates:
		chats = u.Chats
	case *tg.UpdatesCombined:
		chats = u.Chats
	}
	for _, chat := range chats {
		if ch, ok := chat.(*tg.Channel); ok {
			ref := channelRef{ID: ch.ID, AccessHash: ch.AccessHash}
			raw, _ := json.Marshal(ref)
			if err := c.Store.SetSetting("tg_channel", raw); err != nil {
				return err
			}
			c.useChannel(ref)
			return nil
		}
	}
	return errors.New("Kanal wurde erstellt, aber nicht in der Antwort gefunden")
}

func (c *Client) useChannel(ref channelRef) {
	c.mu.Lock()
	c.channel = &ref
	c.mu.Unlock()
	c.account().setChannel(&tg.InputChannel{ChannelID: ref.ID, AccessHash: ref.AccessHash})
}

func (c *Client) client() *tg.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.api
}

func (c *Client) authClient() (*auth.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.auth == nil {
		return nil, errors.New("noch keine Verbindung zu Telegram")
	}
	return c.auth, nil
}

// SendCode starts the login.
func (c *Client) SendCode(ctx context.Context, phone string) error {
	a, err := c.authClient()
	if err != nil {
		return err
	}
	sent, err := a.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return friendly(err)
	}
	code, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return errors.New("unerwartete Antwort von Telegram")
	}
	c.mu.Lock()
	c.phone, c.codeHash = phone, code.PhoneCodeHash
	c.mu.Unlock()
	c.setStatus(Status{Step: StepCode})
	return nil
}

// SignIn submits the login code.
func (c *Client) SignIn(ctx context.Context, code string) error {
	a, err := c.authClient()
	if err != nil {
		return err
	}
	c.mu.RLock()
	phone, hash := c.phone, c.codeHash
	c.mu.RUnlock()
	res, err := a.SignIn(ctx, phone, code, hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		c.setStatus(Status{Step: StepPassword})
		return nil
	}
	if err != nil {
		return friendly(err)
	}
	return c.authorized(ctx, res)
}

// Password submits the Telegram two-step password.
func (c *Client) Password(ctx context.Context, password string) error {
	a, err := c.authClient()
	if err != nil {
		return err
	}
	res, err := a.Password(ctx, password)
	if err != nil {
		return friendly(err)
	}
	return c.authorized(ctx, res)
}

func (c *Client) authorized(ctx context.Context, res *tg.AuthAuthorization) error {
	user, ok := res.User.(*tg.User)
	if !ok {
		return errors.New("unerwartete Antwort von Telegram")
	}
	return c.finishLogin(ctx, user)
}

func friendly(err error) error {
	switch {
	case tgerr.Is(err, "PHONE_NUMBER_INVALID"):
		return errors.New("Telefonnummer ist ungültig, bitte mit Ländervorwahl eingeben")
	case tgerr.Is(err, "PHONE_CODE_INVALID"):
		return errors.New("Code ist falsch")
	case tgerr.Is(err, "PHONE_CODE_EXPIRED"):
		return errors.New("Code ist abgelaufen, bitte neu anfordern")
	case errors.Is(err, auth.ErrPasswordInvalid), tgerr.Is(err, "PASSWORD_HASH_INVALID"):
		return errors.New("Telegram-Passwort ist falsch")
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return fmt.Errorf("Telegram verlangt eine Pause von %s", d.Round(time.Minute))
	}
	return err
}

func (c *Client) ready() (*tg.Client, *channelRef, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.status.Step != StepReady || c.api == nil || c.channel == nil {
		return nil, nil, files.ErrNotReady
	}
	return c.api, c.channel, nil
}

// Put implements files.Backend.
func (c *Client) Put(ctx context.Context, name, mimeType string, r io.Reader, size int64, progress func(int64)) (int, error) {
	api, ch, err := c.ready()
	if err != nil {
		return 0, err
	}
	up := uploader.NewUploader(api).WithPartSize(uploadPart).WithThreads(uploadThreads)
	if progress != nil {
		up = up.WithProgress(progressFunc(progress))
	}
	file, err := up.Upload(ctx, uploader.NewUpload(name, r, size))
	if err != nil {
		return 0, fmt.Errorf("upload: %w", err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 0, err
	}
	res, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Media: &tg.InputMediaUploadedDocument{
			ForceFile:  true,
			File:       file,
			MimeType:   mimeType,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}},
		},
		RandomID: int64(binary.BigEndian.Uint64(random[:])),
	})
	if err != nil {
		return 0, fmt.Errorf("send: %w", err)
	}
	msg := sentMessage(res)
	if msg == nil {
		return 0, errors.New("gesendete Nachricht nicht in der Antwort gefunden")
	}
	doc, err := documentOf(msg)
	if err != nil {
		return 0, err
	}
	if doc.Size != size {
		// Do not trust the message; the caller retries with a new one.
		c.Delete(ctx, []int{msg.ID})
		return 0, fmt.Errorf("Telegram meldet %d statt %d Bytes", doc.Size, size)
	}
	c.account().remember(msg.ID, doc)
	return msg.ID, nil
}

type progressFunc func(int64)

func (p progressFunc) Chunk(_ context.Context, state uploader.ProgressState) error {
	p(state.Uploaded)
	return nil
}

func sentMessage(res tg.UpdatesClass) *tg.Message {
	var updates []tg.UpdateClass
	switch u := res.(type) {
	case *tg.Updates:
		updates = u.Updates
	case *tg.UpdatesCombined:
		updates = u.Updates
	}
	for _, update := range updates {
		if m, ok := update.(*tg.UpdateNewChannelMessage); ok {
			if msg, ok := m.Message.(*tg.Message); ok {
				return msg
			}
		}
	}
	return nil
}

func documentOf(msg *tg.Message) (*tg.Document, error) {
	media, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, fmt.Errorf("Nachricht %d enthält kein Dokument: %w", msg.ID, files.ErrMissing)
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return nil, fmt.Errorf("Nachricht %d: %w", msg.ID, files.ErrMissing)
	}
	return doc, nil
}

// Sizes implements files.Backend.
func (c *Client) Sizes(ctx context.Context, msgIDs []int) (map[int]int64, error) {
	api, ch, err := c.ready()
	if err != nil {
		return nil, err
	}
	sizes := make(map[int]int64, len(msgIDs))
	for start := 0; start < len(msgIDs); start += sizeBatch {
		batch := msgIDs[start:min(start+sizeBatch, len(msgIDs))]
		ids := make([]tg.InputMessageClass, len(batch))
		for i, id := range batch {
			ids[i] = &tg.InputMessageID{ID: id}
		}
		res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
			ID:      ids,
		})
		if err != nil {
			return nil, err
		}
		list, ok := res.(*tg.MessagesChannelMessages)
		if !ok {
			return nil, errors.New("unerwartete Antwort von Telegram")
		}
		for _, m := range list.Messages {
			msg, ok := m.(*tg.Message)
			if !ok {
				continue
			}
			if doc, err := documentOf(msg); err == nil {
				c.account().remember(msg.ID, doc)
				sizes[msg.ID] = doc.Size
			}
		}
	}
	return sizes, nil
}

// Delete implements files.Backend.
func (c *Client) Delete(ctx context.Context, msgIDs []int) error {
	api, ch, err := c.ready()
	if err != nil {
		return err
	}
	_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      msgIDs,
	})
	for _, f := range c.allFetchers() {
		for _, id := range msgIDs {
			f.docs.Delete(id)
		}
	}
	return err
}
