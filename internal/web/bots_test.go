package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"telegram-explorer/internal/telegram"
)

type fakeBots struct{ list []telegram.Bot }

func (f *fakeBots) Bots() []telegram.Bot { return f.list }

func (f *fakeBots) AddBot(_ context.Context, token string) (telegram.Bot, error) {
	if token != "1:good" {
		return telegram.Bot{}, errors.New("der Bot-Token ist ungültig")
	}
	b := telegram.Bot{ID: 1, Username: "added_bot", Source: telegram.BotFromWeb, State: telegram.BotReady}
	f.list = append(f.list, b)
	return b, nil
}

func (f *fakeBots) CreateBot(ctx context.Context) (telegram.Bot, error) {
	return f.AddBot(ctx, "1:good")
}

func (f *fakeBots) RemoveBot(_ context.Context, id int64) error {
	for i, b := range f.list {
		if b.ID == id {
			f.list = append(f.list[:i], f.list[i+1:]...)
			return nil
		}
	}
	return errors.New("diesen Bot gibt es nicht")
}

func TestBotsAPI(t *testing.T) {
	e := newEnv(t)
	e.setup()

	// Without Telegram the settings hide the section.
	var settings map[string]any
	json.Unmarshal(e.must("GET", "/api/settings", nil), &settings)
	if settings["bots"] != false {
		t.Fatalf("bots offered without Telegram: %v", settings["bots"])
	}
	if res, _ := e.do("GET", "/api/settings/bots", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("listing without Telegram: %d", res.StatusCode)
	}

	e.server.Bots = &fakeBots{}
	if res, _ := e.do("POST", "/api/settings/bots", map[string]string{"token": "1:bad"}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad token: %d", res.StatusCode)
	}
	var b telegram.Bot
	json.Unmarshal(e.must("POST", "/api/settings/bots", map[string]string{}), &b)
	if b.Username != "added_bot" || b.State != telegram.BotReady {
		t.Fatalf("created %+v", b)
	}
	var list []telegram.Bot
	json.Unmarshal(e.must("GET", "/api/settings/bots", nil), &list)
	if len(list) != 1 {
		t.Fatalf("%d bots listed", len(list))
	}
	e.must("DELETE", "/api/settings/bots/1", nil)
	if res, _ := e.do("DELETE", "/api/settings/bots/1", nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("removing twice: %d", res.StatusCode)
	}
	if res, _ := e.do("DELETE", "/api/settings/bots/x", nil); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: %d", res.StatusCode)
	}
}
