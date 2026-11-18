package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"telegram-explorer/internal/auth"
)

// bearer sends a request with an app token and no cookie or CSRF header.
func (e *env) bearer(method, path, tok, body string) int {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func (e *env) appLogin(device string) string {
	e.t.Helper()
	var out struct{ Token string }
	raw := e.must("POST", "/api/login", map[string]any{"username": "martin", "password": "kennwort123", "app": true, "device": device})
	json.Unmarshal(raw, &out)
	if out.Token == "" {
		e.t.Fatalf("no token: %s", raw)
	}
	return out.Token
}

func TestAppLoginSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	e.setup()
	tok := e.appLogin("Xiaomi 15")
	if code := e.bearer("POST", "/api/nodes/1/mkdir", tok, `{"name":"vom Handy"}`); code != http.StatusOK {
		t.Fatalf("bearer mkdir: %d", code)
	}
	if code := e.bearer("GET", "/api/nodes/1/children", tok+"x", ""); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", code)
	}

	// A restart loses the vault key and the manager, but not the logins.
	e.server.Auth = auth.New(e.engine.Store)
	e.engine.SetVault(nil)
	if code := e.bearer("GET", "/api/nodes/1/children", tok, ""); code != http.StatusLocked {
		t.Fatalf("after restart: %d", code)
	}
	if code := e.bearer("POST", "/api/unlock", tok, `{"passphrase":"passphrase123"}`); code != http.StatusOK {
		t.Fatalf("unlock with token: %d", code)
	}
	e.must("GET", "/api/nodes/1/children", nil) // the browser cookie survived too
}

func TestRevokeSessions(t *testing.T) {
	e := newEnv(t)
	e.setup()
	phone := e.appLogin("Xiaomi 15")
	tablet := e.appLogin("Tablet")

	var list []struct {
		ID, Kind, Name string
		Current        bool
	}
	json.Unmarshal(e.must("GET", "/api/sessions", nil), &list)
	if len(list) != 3 {
		t.Fatalf("sessions: %+v", list)
	}
	var phoneID string
	current := 0
	for _, se := range list {
		if se.Name == "Xiaomi 15" && se.Kind == "app" {
			phoneID = se.ID
		}
		if se.Current {
			current++
		}
	}
	if phoneID == "" || current != 1 {
		t.Fatalf("sessions: %+v", list)
	}
	e.must("DELETE", "/api/sessions/"+phoneID, nil)
	if code := e.bearer("GET", "/api/nodes/1/children", phone, ""); code != http.StatusUnauthorized {
		t.Fatalf("revoked phone: %d", code)
	}
	if code := e.bearer("GET", "/api/nodes/1/children", tablet, ""); code != http.StatusOK {
		t.Fatalf("tablet: %d", code)
	}

	// A new password ends every other login, but not the one that changed it.
	e.must("POST", "/api/settings/password", map[string]string{"current": "kennwort123", "password": "neueskennwort"})
	if code := e.bearer("GET", "/api/nodes/1/children", tablet, ""); code != http.StatusUnauthorized {
		t.Fatalf("tablet after password change: %d", code)
	}
	e.must("GET", "/api/nodes/1/children", nil)
}

func TestBrowserName(t *testing.T) {
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36 Edg/140.0"
	if got := browserName(ua); got != "Edge unter Windows" {
		t.Fatal(got)
	}
}
