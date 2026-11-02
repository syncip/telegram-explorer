package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"telegram-explorer/internal/store"
)

// Helper bots download alongside the account. Telegram allows every login
// about five file parts a second, so each bot that is an admin of the storage
// channel adds that much again.

// Where a helper bot was configured.
const (
	BotFromEnv = "env"
	BotFromWeb = "web"
)

// States of a helper bot.
const (
	BotConnecting = "connecting"
	BotReady      = "ready"
	BotError      = "error"
)

const (
	botsSetting = "tg_bots"
	// maxBots is what one account may own at @BotFather.
	maxBots       = 20
	botFatherWait = 15 * time.Second
)

var tokenPattern = regexp.MustCompile(`\d{5,}:[A-Za-z0-9_-]{30,}`)

// Bot describes a helper bot for the settings.
type Bot struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
	Source   string `json:"source"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

type bot struct {
	token   string
	f       *fetcher
	cancel  context.CancelFunc
	restart chan error

	mu   sync.Mutex
	info Bot
}

func (b *bot) set(state, errText string) {
	b.mu.Lock()
	b.info.State, b.info.Error = state, errText
	b.mu.Unlock()
}

func (b *bot) snapshot() Bot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.info
}

func botID(token string) (int64, error) {
	if token == "" || tokenPattern.FindString(token) != token {
		return 0, errors.New("das ist kein Bot-Token, er sieht so aus: 123456789:ABC…")
	}
	return strconv.ParseInt(token[:strings.IndexByte(token, ':')], 10, 64)
}

// botSession keeps a bot's MTProto session sealed, so a restart does not log
// in again. Telegram limits bot logins.
type botSession struct {
	c  *Client
	id int64
}

func (s botSession) key() string { return fmt.Sprintf("tg_bot_session_%d", s.id) }

func (s botSession) LoadSession(context.Context) ([]byte, error) {
	sealed, err := s.c.Store.Setting(s.key())
	if err != nil {
		return nil, err
	}
	if sealed == nil {
		return nil, session.ErrNotFound
	}
	return s.c.Vault().Open("tg-bot-session", sealed)
}

func (s botSession) StoreSession(_ context.Context, data []byte) error {
	sealed, err := s.c.Vault().Seal("tg-bot-session", data)
	if err != nil {
		return err
	}
	return s.c.Store.SetSetting(s.key(), sealed)
}

// startBots starts the helpers from the environment and from the settings.
func (c *Client) startBots(ctx context.Context) {
	c.botsMu.Lock()
	c.botCtx = ctx
	c.botsMu.Unlock()
	for _, token := range c.BotTokens {
		if token = strings.TrimSpace(token); token == "" {
			continue
		}
		if _, err := c.addBot(token, BotFromEnv); err != nil {
			log.Printf("TG_BOTS: %v", err)
		}
	}
	tokens, err := c.webTokens()
	if err != nil {
		c.Store.Log(store.LevelError, store.KindTelegram, "Download-Helfer nicht lesbar: %v", err)
	}
	for _, token := range tokens {
		if _, err := c.addBot(token, BotFromWeb); err != nil {
			log.Printf("Download-Helfer: %v", err)
		}
	}
}

func (c *Client) addBot(token, source string) (*bot, error) {
	id, err := botID(token)
	if err != nil {
		return nil, err
	}
	c.botsMu.Lock()
	defer c.botsMu.Unlock()
	if c.botCtx == nil {
		return nil, errors.New("Telegram ist noch nicht gestartet")
	}
	for _, b := range c.bots {
		if b.info.ID == id {
			return nil, errors.New("dieser Bot ist schon eingetragen")
		}
	}
	if len(c.bots) >= maxBots {
		return nil, fmt.Errorf("mehr als %d Download-Helfer gehen nicht", maxBots)
	}
	ctx, cancel := context.WithCancel(c.botCtx)
	b := &bot{
		token:   token,
		f:       c.newFetcher(fmt.Sprintf("Bot %d", id)),
		cancel:  cancel,
		restart: make(chan error, 1),
		info:    Bot{ID: id, Source: source, State: BotConnecting},
	}
	c.bots = append(c.bots, b)
	go c.runBot(ctx, b)
	return b, nil
}

func (c *Client) webTokens() ([]string, error) {
	sealed, err := c.Store.Setting(botsSetting)
	if err != nil || sealed == nil {
		return nil, err
	}
	raw, err := c.Vault().Open("tg-bots", sealed)
	if err != nil {
		return nil, err
	}
	var tokens []string
	return tokens, json.Unmarshal(raw, &tokens)
}

// persist stores the tokens of the bots added in the web interface. c.botsMu
// must be held.
func (c *Client) persist() error {
	tokens := []string{}
	for _, b := range c.bots {
		if b.info.Source == BotFromWeb {
			tokens = append(tokens, b.token)
		}
	}
	raw, _ := json.Marshal(tokens)
	sealed, err := c.Vault().Seal("tg-bots", raw)
	if err != nil {
		return err
	}
	return c.Store.SetSetting(botsSetting, sealed)
}

func (c *Client) runBot(ctx context.Context, b *bot) {
	delay := 5 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := c.runBotOnce(ctx, b)
		b.f.detach()
		if ctx.Err() != nil {
			return
		}
		b.set(BotError, err.Error())
		c.Store.Log(store.LevelWarn, store.KindTelegram, "Download-Helfer %s: %v", b.f.label(), err)
		if time.Since(started) > 5*time.Minute {
			delay = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 10*time.Minute)
	}
}

func (c *Client) runBotOnce(ctx context.Context, b *bot) error {
	client := telegram.NewClient(c.AppID, c.AppHash, telegram.Options{
		SessionStorage: botSession{c: c, id: b.info.ID},
		Middlewares:    []telegram.Middleware{floodWait{where: "Hauptverbindung von " + b.f.label(), onWait: b.f.flooded}, c.transferRetry()},
		NoUpdates:      true,
		Device:         telegram.DeviceConfig{DeviceModel: "Telegram Explorer", SystemVersion: "Docker", AppVersion: "1.0"},
	})
	return client.Run(ctx, func(ctx context.Context) error {
		b.set(BotConnecting, "")
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if !status.Authorized {
			if _, err := client.Auth().Bot(ctx, b.token); err != nil {
				return botError(err)
			}
		}
		self, err := client.Self(ctx)
		if tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_REVOKED") {
			// The token was renewed or revoked; the next try logs in again.
			c.Store.DeleteSetting(botSession{id: b.info.ID}.key())
			return errors.New("die Anmeldung des Bots gilt nicht mehr")
		}
		if err != nil {
			return err
		}
		if !self.Bot {
			return errors.New("der Token gehört zu keinem Bot")
		}
		name := "@" + self.Username
		b.f.setName(name)
		b.mu.Lock()
		b.info.Username = self.Username
		b.mu.Unlock()
		b.f.attach(client)
		ch, err := c.botChannel(ctx, client.API())
		if errors.Is(err, errNoAccess) {
			// Only the account can make the bot an admin.
			if err := c.waitAccount(ctx); err != nil {
				return err
			}
			if err := c.promote(ctx, self.Username); err != nil {
				return fmt.Errorf("kein Zugriff auf den Speicherkanal, und als Admin eintragen ging nicht: %w", err)
			}
			c.Store.Log(store.LevelInfo, store.KindTelegram, "Download-Helfer %s ist jetzt Admin im Speicherkanal", name)
			ch, err = c.botChannel(ctx, client.API())
		}
		if err != nil {
			return err
		}
		b.f.setChannel(ch)
		b.set(BotReady, "")
		c.Store.Log(store.LevelInfo, store.KindTelegram, "Download-Helfer %s ist bereit", name)
		log.Printf("telegram: Download-Helfer %s ist bereit", name)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-b.restart:
			return err
		}
	})
}

// waitAccount waits a while for the account's connection, which starts at
// the same time as the bots.
func (c *Client) waitAccount(ctx context.Context) error {
	for range 60 {
		if c.Ready() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("das Telegram-Konto ist nicht verbunden")
}

func botError(err error) error {
	switch {
	case tgerr.Is(err, "ACCESS_TOKEN_INVALID"):
		return errors.New("der Bot-Token ist ungültig")
	case tgerr.Is(err, "ACCESS_TOKEN_EXPIRED"):
		return errors.New("der Bot-Token wurde bei @BotFather erneuert oder widerrufen")
	}
	return friendly(err)
}

// helperFailed takes a bot out of service after a failed read. It connects
// again after a pause.
func (c *Client) helperFailed(f *fetcher, err error) {
	c.botsMu.Lock()
	defer c.botsMu.Unlock()
	for _, b := range c.bots {
		if b.f == f {
			f.detach()
			select {
			case b.restart <- fmt.Errorf("Lesen fehlgeschlagen: %w", err):
			default:
			}
			return
		}
	}
}

var errNoAccess = errors.New("kein Zugriff auf den Speicherkanal")

func (c *Client) storedChannel() (*channelRef, error) {
	raw, err := c.Store.Setting("tg_channel")
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, errors.New("den Speicherkanal gibt es noch nicht, zuerst mit Telegram anmelden")
	}
	var ref channelRef
	return &ref, json.Unmarshal(raw, &ref)
}

// botChannel finds the storage channel as the bot sees it. Bots may name a
// channel without an access hash; the answer carries their own.
func (c *Client) botChannel(ctx context.Context, api *tg.Client) (*tg.InputChannel, error) {
	ref, err := c.storedChannel()
	if err != nil {
		return nil, err
	}
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{&tg.InputChannel{ChannelID: ref.ID}})
	if tgerr.Is(err, "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_ADMIN_REQUIRED", "USER_NOT_PARTICIPANT") {
		return nil, errNoAccess
	}
	if err != nil {
		return nil, err
	}
	for _, chat := range res.GetChats() {
		ch, ok := chat.(*tg.Channel)
		if !ok || ch.ID != ref.ID {
			continue
		}
		if _, admin := ch.GetAdminRights(); !admin || ch.Left {
			return nil, errNoAccess
		}
		return &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
	}
	return nil, errNoAccess
}

func (c *Client) resolveUser(ctx context.Context, api *tg.Client, username string) (*tg.InputUser, error) {
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return nil, err
	}
	for _, u := range res.Users {
		if user, ok := u.(*tg.User); ok && strings.EqualFold(user.Username, username) {
			return &tg.InputUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
	}
	return nil, fmt.Errorf("@%s nicht gefunden", username)
}

func (c *Client) accountChannel() (*tg.Client, *tg.InputChannel, error) {
	api, ch, err := c.ready()
	if err != nil {
		return nil, nil, errors.New("das Telegram-Konto ist nicht verbunden")
	}
	return api, &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, nil
}

// promote makes a bot an admin of the storage channel, which bots need to see
// its messages. "Other" is the smallest right there is; it allows nothing that
// matters here.
func (c *Client) promote(ctx context.Context, username string) error {
	api, ch, err := c.accountChannel()
	if err != nil {
		return err
	}
	user, err := c.resolveUser(ctx, api, username)
	if err != nil {
		return err
	}
	_, err = api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{Channel: ch, UserID: user, AdminRights: tg.ChatAdminRights{Other: true}})
	return err
}

// demote removes a bot from the storage channel.
func (c *Client) demote(ctx context.Context, username string) error {
	api, ch, err := c.accountChannel()
	if err != nil {
		return err
	}
	user, err := c.resolveUser(ctx, api, username)
	if err != nil {
		return err
	}
	if _, err := api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{Channel: ch, UserID: user}); err != nil {
		return err
	}
	peer := &tg.InputPeerUser{UserID: user.UserID, AccessHash: user.AccessHash}
	if _, err := api.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{Channel: ch, Participant: peer, BannedRights: tg.ChatBannedRights{ViewMessages: true}}); err != nil {
		return err
	}
	// Lift the ban again, so the bot can be added back later.
	_, err = api.ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{Channel: ch, Participant: peer})
	return err
}

// Bots lists the helper bots.
func (c *Client) Bots() []Bot {
	c.botsMu.Lock()
	defer c.botsMu.Unlock()
	out := []Bot{}
	for _, b := range c.bots {
		out = append(out, b.snapshot())
	}
	return out
}

// AddBot adds a helper bot by its token and waits until it is ready or failed.
func (c *Client) AddBot(ctx context.Context, token string) (Bot, error) {
	b, err := c.addBot(strings.TrimSpace(token), BotFromWeb)
	if err != nil {
		return Bot{}, err
	}
	c.botsMu.Lock()
	err = c.persist()
	c.botsMu.Unlock()
	if err != nil {
		c.RemoveBot(ctx, b.info.ID)
		return Bot{}, err
	}
	c.Store.Log(store.LevelInfo, store.KindTelegram, "Download-Helfer %d eingetragen", b.info.ID)
	deadline := time.After(20 * time.Second)
	for {
		if info := b.snapshot(); info.State != BotConnecting {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return b.snapshot(), nil
		case <-deadline:
			return b.snapshot(), nil
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// RemoveBot stops a helper bot added in the web interface and takes it out of
// the storage channel. The bot itself stays at @BotFather.
func (c *Client) RemoveBot(ctx context.Context, id int64) error {
	c.botsMu.Lock()
	var gone *bot
	for i, b := range c.bots {
		if b.info.ID == id {
			if b.info.Source == BotFromEnv {
				c.botsMu.Unlock()
				return errors.New("dieser Bot steht in der .env, entferne ihn dort")
			}
			gone = b
			c.bots = append(c.bots[:i:i], c.bots[i+1:]...)
			break
		}
	}
	if gone == nil {
		c.botsMu.Unlock()
		return errors.New("diesen Bot gibt es nicht")
	}
	err := c.persist()
	c.botsMu.Unlock()
	gone.cancel()
	c.Store.DeleteSetting(botSession{id: id}.key())
	if err != nil {
		return err
	}
	info := gone.snapshot()
	c.Store.Log(store.LevelInfo, store.KindTelegram, "Download-Helfer %d entfernt", id)
	if info.Username != "" {
		if err := c.demote(ctx, info.Username); err != nil {
			return fmt.Errorf("Bot entfernt, aus dem Speicherkanal austragen ging aber nicht: %w", err)
		}
	}
	return nil
}

// CreateBot asks @BotFather for a new bot and adds it as a helper. It relies
// on the wording of @BotFather's answers only as far as it must: the token is
// recognised by its form.
func (c *Client) CreateBot(ctx context.Context) (Bot, error) {
	c.createMu.Lock()
	defer c.createMu.Unlock()
	api, _, err := c.accountChannel()
	if err != nil {
		return Bot{}, err
	}
	token, err := c.botFather(ctx, api)
	if err != nil {
		return Bot{}, err
	}
	return c.AddBot(ctx, token)
}

func (c *Client) botFather(ctx context.Context, api *tg.Client) (string, error) {
	father, err := c.resolveUser(ctx, api, "BotFather")
	if err != nil {
		return "", fmt.Errorf("@BotFather: %w", err)
	}
	peer := &tg.InputPeerUser{UserID: father.UserID, AccessHash: father.AccessHash}
	ask := func(text string) (string, error) { return c.ask(ctx, api, peer, text) }

	// A dialog left open from earlier would swallow the first answer.
	if _, err := ask("/cancel"); err != nil {
		return "", err
	}
	reply, err := ask("/newbot")
	if err != nil {
		return "", err
	}
	if refused(reply) {
		return "", fmt.Errorf("@BotFather: %s", firstLine(reply))
	}
	if reply, err = ask("Telegram Explorer Helfer"); err != nil {
		return "", err
	}
	if refused(reply) {
		return "", fmt.Errorf("@BotFather: %s", firstLine(reply))
	}
	for range 3 {
		var suffix [4]byte
		rand.Read(suffix[:])
		if reply, err = ask("telex_" + hex.EncodeToString(suffix[:]) + "_bot"); err != nil {
			return "", err
		}
		if token := tokenPattern.FindString(reply); token != "" {
			c.Store.Log(store.LevelInfo, store.KindTelegram, "Neuen Bot bei @BotFather angelegt")
			return token, nil
		}
		// A taken name makes @BotFather ask for another one.
		if !strings.Contains(strings.ToLower(reply), "username") {
			break
		}
	}
	return "", fmt.Errorf("@BotFather hat keinen Token geschickt: %s", firstLine(reply))
}

func refused(reply string) bool {
	return strings.HasPrefix(strings.TrimSpace(reply), "Sorry")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// ask sends a message to a bot and waits for its answer.
func (c *Client) ask(ctx context.Context, api *tg.Client, peer *tg.InputPeerUser, text string) (string, error) {
	before, err := history(ctx, api, peer)
	if err != nil {
		return "", err
	}
	last := 0
	for _, m := range before {
		last = max(last, m.ID)
	}
	var random [8]byte
	rand.Read(random[:])
	if _, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: text, RandomID: int64(binary.BigEndian.Uint64(random[:]))}); err != nil {
		return "", err
	}
	deadline := time.Now().Add(botFatherWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
		msgs, err := history(ctx, api, peer)
		if err != nil {
			return "", err
		}
		var answer []string
		for i := len(msgs) - 1; i >= 0; i-- {
			if m := msgs[i]; m.ID > last && !m.Out {
				answer = append(answer, m.Message)
			}
		}
		if len(answer) > 0 {
			return strings.Join(answer, "\n"), nil
		}
	}
	return "", errors.New("@BotFather antwortet nicht")
}

// history returns the latest messages of a chat, newest first.
func history(ctx context.Context, api *tg.Client, peer tg.InputPeerClass) ([]*tg.Message, error) {
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: 10})
	if err != nil {
		return nil, err
	}
	var list []tg.MessageClass
	switch r := res.(type) {
	case *tg.MessagesMessages:
		list = r.Messages
	case *tg.MessagesMessagesSlice:
		list = r.Messages
	}
	var out []*tg.Message
	for _, m := range list {
		if msg, ok := m.(*tg.Message); ok {
			out = append(out, msg)
		}
	}
	return out, nil
}
