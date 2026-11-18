package web

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"telegram-explorer/internal/auth"
)

// nextEvent reads until an event with this name arrives.
func nextEvent(t *testing.T, sc *bufio.Scanner, name string) string {
	t.Helper()
	current := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && current == name:
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("stream ended before %q: %v", name, sc.Err())
	return ""
}

func TestEventStream(t *testing.T) {
	e := newEnv(t)
	e.setup()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/events", nil)
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	sc := bufio.NewScanner(res.Body)
	if got := nextEvent(t, sc, "state"); !strings.Contains(got, `"locked":false`) {
		t.Fatalf("state %s", got)
	}
	first := nextEvent(t, sc, "files")

	// A log line alone is no change to the files.
	e.engine.Store.Log("info", "system", "nur ein Protokolleintrag")
	e.must("POST", "/api/nodes/1/mkdir", map[string]string{"name": "Neu"})
	if got := nextEvent(t, sc, "files"); got == first {
		t.Fatal("no files event after mkdir")
	}
	if got := nextEvent(t, sc, "stats"); !strings.Contains(got, `"folders":`) {
		t.Fatalf("stats %s", got)
	}

	e.engine.SetVault(nil)
	if got := nextEvent(t, sc, "state"); !strings.Contains(got, `"locked":true`) {
		t.Fatalf("state after lock %s", got)
	}

	// Without a session there is no stream.
	plain, err := http.Get(e.srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	plain.Body.Close()
	if plain.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", plain.StatusCode)
	}
}

func TestFileChangesIgnoreLog(t *testing.T) {
	e := newEnv(t)
	e.setup()
	before, _ := e.engine.Store.FileChanges()
	e.engine.Store.Log("info", "system", "a")
	e.auth.NewSession(deviceForTest())
	after, _ := e.engine.Store.FileChanges()
	if before != after {
		t.Fatalf("%d != %d", before, after)
	}
}

func deviceForTest() auth.Device { return auth.Device{App: true, Name: "Test"} }
