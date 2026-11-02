package web

import (
	"context"
	"net/http"
	"strconv"

	"telegram-explorer/internal/telegram"
)

// HelperBots manages the bots that download alongside the account.
type HelperBots interface {
	Bots() []telegram.Bot
	AddBot(ctx context.Context, token string) (telegram.Bot, error)
	CreateBot(ctx context.Context) (telegram.Bot, error)
	RemoveBot(ctx context.Context, id int64) error
}

func (s *Server) listBots(w http.ResponseWriter, r *http.Request) error {
	if s.Bots == nil {
		return httpError{http.StatusNotFound, "Download-Helfer gibt es nur mit Telegram"}
	}
	return writeJSON(w, s.Bots.Bots())
}

// addBot takes a token, or without one has @BotFather create a new bot.
func (s *Server) addBot(w http.ResponseWriter, r *http.Request) error {
	if s.Bots == nil {
		return httpError{http.StatusNotFound, "Download-Helfer gibt es nur mit Telegram"}
	}
	var in struct{ Token string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	var (
		bot telegram.Bot
		err error
	)
	if in.Token == "" {
		bot, err = s.Bots.CreateBot(r.Context())
	} else {
		bot, err = s.Bots.AddBot(r.Context(), in.Token)
	}
	if err != nil {
		return badRequest(err.Error())
	}
	return writeJSON(w, bot)
}

func (s *Server) removeBot(w http.ResponseWriter, r *http.Request) error {
	if s.Bots == nil {
		return httpError{http.StatusNotFound, "Download-Helfer gibt es nur mit Telegram"}
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return badRequest("ungültige Bot-ID")
	}
	if err := s.Bots.RemoveBot(r.Context(), id); err != nil {
		return badRequest(err.Error())
	}
	return writeJSON(w, map[string]bool{"ok": true})
}
