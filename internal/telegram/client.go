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
	"strings"
	"sync"
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
	fetched  time.Time
}

// Client implements files.Backend.
type Client struct {
	AppID   int
	AppHash string
	Store   *store.Store
	Vault   func() *vault.Vault

	mu       sync.RWMutex
	status   Status
	api      *tg.Client
	auth     *auth.Client
	channel  *channelRef
	phone    string
	codeHash string

	docs sync.Map // msg id -> docRef
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
type floodWait struct{}

func (floodWait) Handle(next tg.Invoker) telegram.InvokeFunc {
	return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
		for {
			err := next.Invoke(ctx, input, output)
			d, ok := tgerr.AsFloodWait(err)
			if !ok || d > maxFloodWait {
				return err
			}
			log.Printf("telegram: Limit erreicht, warte %s", d)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d + time.Second):
			}
		}
	}
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
		Middlewares:    []telegram.Middleware{floodWait{}},
		Device:         telegram.DeviceConfig{DeviceModel: "Telegram Explorer", SystemVersion: "Docker", AppVersion: "1.0"},
	})
	return client.Run(ctx, func(ctx context.Context) error {
		c.mu.Lock()
		c.api, c.auth = client.API(), client.Auth()
		c.mu.Unlock()
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
	c.Store.Log(store.LevelInfo, store.KindTelegram, "Verbunden als %s", name)
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
		c.mu.Lock()
		c.channel = &ref
		c.mu.Unlock()
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
			c.mu.Lock()
			c.channel = &ref
			c.mu.Unlock()
			return nil
		}
	}
	return errors.New("Kanal wurde erstellt, aber nicht in der Antwort gefunden")
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
	c.remember(msg.ID, doc)
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

func (c *Client) remember(msgID int, doc *tg.Document) *tg.InputDocumentFileLocation {
	loc := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
	c.docs.Store(msgID, docRef{location: loc, fetched: time.Now()})
	return loc
}

// location returns a document location. File references expire, so they are
// refreshed from the message when old or when Telegram rejects them.
func (c *Client) location(ctx context.Context, api *tg.Client, ch *channelRef, msgID int, fresh bool) (*tg.InputDocumentFileLocation, error) {
	if v, ok := c.docs.Load(msgID); ok && !fresh {
		if ref := v.(docRef); time.Since(ref.fetched) < docCacheTTL {
			return ref.location, nil
		}
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: msgID}},
	})
	if err != nil {
		return nil, err
	}
	list, ok := res.(*tg.MessagesChannelMessages)
	if !ok {
		return nil, errors.New("unerwartete Antwort von Telegram")
	}
	for _, m := range list.Messages {
		if msg, ok := m.(*tg.Message); ok && msg.ID == msgID {
			doc, err := documentOf(msg)
			if err != nil {
				return nil, err
			}
			return c.remember(msgID, doc), nil
		}
	}
	return nil, fmt.Errorf("Nachricht %d: %w", msgID, files.ErrMissing)
}

// ReadPart implements files.Backend.
func (c *Client) ReadPart(ctx context.Context, msgID int, part int64) ([]byte, error) {
	api, ch, err := c.ready()
	if err != nil {
		return nil, err
	}
	fresh := false
	for attempt := 0; ; attempt++ {
		loc, err := c.location(ctx, api, ch, msgID, fresh)
		if err != nil {
			return nil, err
		}
		res, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: loc,
			Offset:   part * files.PartSize,
			Limit:    files.PartSize,
		})
		if tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID") && attempt == 0 {
			fresh = true
			continue
		}
		if err != nil {
			return nil, err
		}
		file, ok := res.(*tg.UploadFile)
		if !ok {
			return nil, fmt.Errorf("unerwartete Antwort von Telegram: %T", res)
		}
		return file.Bytes, nil
	}
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
				c.remember(msg.ID, doc)
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
	for _, id := range msgIDs {
		c.docs.Delete(id)
	}
	return err
}
