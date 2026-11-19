package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
	"telegram-explorer/internal/telegram"
)

const (
	eventTick  = 700 * time.Millisecond
	eventPing  = 20 * time.Second
	eventCheck = 30 * time.Second
)

// streams ends every open event stream when the server shuts down.
type streams struct {
	once sync.Once
	done chan struct{}
}

func (s *streams) init() { s.once.Do(func() { s.done = make(chan struct{}) }) }

// CloseStreams ends the open event streams, so a shutdown does not wait for
// them, and stores what is open in the live editor.
func (s *Server) CloseStreams() {
	s.FlushLive()
	s.streams.init()
	select {
	case <-s.streams.done:
	default:
		close(s.streams.done)
	}
}

// events streams what changed as server-sent events, instead of the clients
// polling several endpoints every few seconds:
//
//	state  {locked, telegram}         vault or Telegram login changed
//	files  {v}                        files, folders or shares changed
//	stats  {...stats, speed}          totals or transfer speed changed
//	jobs   [...]                      upload queue changed
//	verify {...}                      check progress changed
//	ping   {}                         every 20 seconds
//	logout {}                         the session ended
//
// Every event is sent once right after connecting.
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return badRequest("Streaming nicht möglich")
	}
	s.streams.init()
	tok, _ := token(r)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	last := map[string][]byte{}
	send := func(name string, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if bytes.Equal(last[name], data) {
			return nil
		}
		last[name] = data
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	var stats *store.Stats
	changes := int64(-1)
	tick := time.NewTicker(eventTick)
	defer tick.Stop()
	lastPing, lastCheck := time.Now(), time.Now()
	for {
		locked := s.Engine.Vault() == nil
		state := map[string]any{"locked": locked}
		if s.Telegram == nil {
			state["telegram"] = telegram.Status{Step: telegram.StepReady, User: "Lokaler Testspeicher"}
		} else if !locked {
			state["telegram"] = s.Telegram.Status()
		}
		if err := send("state", state); err != nil {
			return nil
		}
		if !locked {
			n, err := s.Engine.Store.FileChanges()
			if err != nil {
				return nil
			}
			if n != changes {
				changes = n
				if stats, err = s.Engine.Store.Stats(); err != nil {
					return nil
				}
				if err := send("files", map[string]int64{"v": n}); err != nil {
					return nil
				}
			}
			jobs, err := s.Engine.Jobs()
			if err != nil {
				return nil
			}
			if send("jobs", jobs) != nil ||
				send("verify", s.Engine.VerifyState()) != nil ||
				send("stats", struct {
					*store.Stats
					Speed files.Speed `json:"speed"`
				}{stats, s.Engine.Speed()}) != nil {
				return nil
			}
		}

		select {
		case <-r.Context().Done():
			return nil
		case <-s.streams.done:
			return nil
		case now := <-tick.C:
			if now.Sub(lastCheck) >= eventCheck {
				lastCheck = now
				if !s.Auth.ValidSession(tok) {
					fmt.Fprint(w, "event: logout\ndata: {}\n\n")
					flusher.Flush()
					return nil
				}
			}
			if now.Sub(lastPing) >= eventPing {
				lastPing = now
				// A real event, so clients can tell a live stream from one
				// that a buffering proxy holds back.
				if _, err := fmt.Fprint(w, "event: ping\ndata: {}\n\n"); err != nil {
					return nil
				}
				flusher.Flush()
			}
		}
	}
}
