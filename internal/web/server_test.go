package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/files"
	"telegram-explorer/internal/sftpd"
	"telegram-explorer/internal/store"
)

type env struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	engine *files.Engine
	auth   *auth.Manager
	server *Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	backend, err := files.NewLocalBackend(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := files.New(st, backend, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	manager := auth.New(st)
	s := &Server{Engine: engine, Auth: manager, SetupCode: "code", UI: fstest.MapFS{"index.html": {Data: []byte("ui")}},
		OnUnlock: func() { engine.Start(ctx) }}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &env{t: t, srv: srv, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, engine: engine, auth: manager, server: s}
}

func (e *env) do(method, path string, body any, headers ...string) (*http.Response, []byte) {
	e.t.Helper()
	var reader io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		reader = bytes.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("X-Telex", "1")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res, data
}

func (e *env) must(method, path string, body any, headers ...string) []byte {
	e.t.Helper()
	res, data := e.do(method, path, body, headers...)
	if res.StatusCode >= 300 {
		e.t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, data)
	}
	return data
}

func (e *env) setup() {
	e.t.Helper()
	e.must("POST", "/api/setup", map[string]string{"setupCode": "code", "username": "martin", "password": "kennwort123", "passphrase": "passphrase123"})
}

// upload sends a file in two chunks and returns the node id.
func (e *env) upload(parent int64, name string, data []byte, encrypted bool) int64 {
	e.t.Helper()
	var created struct{ ID string }
	raw := e.must("POST", "/api/uploads", map[string]any{"parentId": parent, "name": name, "size": len(data), "encrypted": encrypted})
	json.Unmarshal(raw, &created)
	half := len(data) / 2
	e.must("PATCH", "/api/uploads/"+created.ID, data[:half], "X-Upload-Offset", "0")
	var done struct {
		Node struct{ ID int64 }
	}
	raw = e.must("PATCH", "/api/uploads/"+created.ID, data[half:], "X-Upload-Offset", fmt.Sprint(half))
	json.Unmarshal(raw, &done)
	return done.Node.ID
}

func (e *env) waitReady(id int64) {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n, err := e.engine.Store.Get(id)
		if err == nil && n.State == store.StateReady {
			return
		}
		e.engine.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("node %d not ready", id)
}

func TestAccessRules(t *testing.T) {
	e := newEnv(t)
	if res, _ := e.do("GET", "/api/nodes/1/children", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no session: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/api/setup", map[string]string{"setupCode": "falsch", "username": "x", "password": "kennwort123", "passphrase": "passphrase123"}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong setup code: %d", res.StatusCode)
	}
	e.setup()
	if res, _ := e.do("POST", "/api/setup", map[string]string{"setupCode": "code", "username": "x", "password": "kennwort123", "passphrase": "passphrase123"}); res.StatusCode != http.StatusConflict {
		t.Fatalf("second setup: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/nodes/1/mkdir", strings.NewReader(`{"name":"x"}`))
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF header: %d", res.StatusCode)
	}
}

func TestLockAfterRestart(t *testing.T) {
	e := newEnv(t)
	e.setup()
	e.engine.SetVault(nil)
	if res, _ := e.do("GET", "/api/nodes/1/children", nil); res.StatusCode != http.StatusLocked {
		t.Fatalf("locked vault: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/api/unlock", map[string]string{"passphrase": "falsch"}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong passphrase: %d", res.StatusCode)
	}
	e.must("POST", "/api/unlock", map[string]string{"passphrase": "passphrase123"})
	e.must("GET", "/api/nodes/1/children", nil)
}

func TestLoginLockout(t *testing.T) {
	e := newEnv(t)
	e.setup()
	var last int
	for range 8 {
		res, _ := e.do("POST", "/api/login", map[string]string{"username": "martin", "password": "falsch"})
		last = res.StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("status %d", last)
	}
}

func TestUploadDownloadRange(t *testing.T) {
	e := newEnv(t)
	e.setup()
	data := make([]byte, 3<<20)
	rand.Read(data)
	id := e.upload(store.RootID, "video.mp4", data, true)
	e.waitReady(id)

	if got := e.must("GET", fmt.Sprintf("/api/nodes/%d/content", id), nil); !bytes.Equal(got, data) {
		t.Fatal("download differs")
	}
	res, got := e.do("GET", fmt.Sprintf("/api/nodes/%d/content", id), nil, "Range", "bytes=2097000-2097999")
	if res.StatusCode != http.StatusPartialContent || !bytes.Equal(got, data[2097000:2098000]) {
		t.Fatalf("range: %d", res.StatusCode)
	}
	if res, _ := e.do("POST", "/api/uploads", map[string]any{"parentId": 1, "name": "video.mp4", "size": 1}); res.StatusCode != http.StatusConflict {
		t.Fatalf("conflict: %d", res.StatusCode)
	}
}

func TestHTMLIsNeverServedInline(t *testing.T) {
	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "evil.html", []byte("<script>alert(1)</script>"), true)
	res, _ := e.do("GET", fmt.Sprintf("/api/nodes/%d/content?inline=1", id), nil)
	if !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") || strings.Contains(res.Header.Get("Content-Type"), "html") {
		t.Fatalf("headers: %v", res.Header)
	}
}

func TestShareLimits(t *testing.T) {
	e := newEnv(t)
	e.setup()
	data := []byte("geteilte Datei")
	id := e.upload(store.RootID, "a.txt", data, true)
	e.waitReady(id)

	var share struct{ Token string }
	json.Unmarshal(e.must("POST", "/api/shares", map[string]any{"nodeId": id, "password": "geheim", "maxDownloads": 1}), &share)

	visitorJar, _ := cookiejar.New(nil)
	visitor := &env{t: t, srv: e.srv, client: &http.Client{Jar: visitorJar, CheckRedirect: e.client.CheckRedirect}}

	if res, _ := visitor.do("GET", "/s/"+share.Token+"/dl", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("without password: %d", res.StatusCode)
	}
	form := []byte("password=falsch")
	if _, body := visitor.do("POST", "/s/"+share.Token, form, "Content-Type", "application/x-www-form-urlencoded"); !strings.Contains(string(body), "falsch") {
		t.Fatal("wrong password accepted")
	}
	form = []byte("password=geheim")
	if res, _ := visitor.do("POST", "/s/"+share.Token, form, "Content-Type", "application/x-www-form-urlencoded"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("password: %d", res.StatusCode)
	}
	// A range request must not dodge the download limit.
	if res, got := visitor.do("GET", "/s/"+share.Token+"/dl", nil, "Range", "bytes=1-"); res.StatusCode != 200 || !bytes.Equal(got, data) {
		t.Fatalf("first download: %d %q", res.StatusCode, got)
	}
	if res, _ := visitor.do("GET", "/s/"+share.Token+"/dl", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("over the limit: %d", res.StatusCode)
	}

	e.must("POST", "/api/delete", map[string]any{"ids": []int64{id}})
	if res, _ := visitor.do("GET", "/s/"+share.Token, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("trashed: %d", res.StatusCode)
	}
}

func TestUpdateShare(t *testing.T) {
	e := newEnv(t)
	e.setup()
	data := []byte("freigabe")
	id := e.upload(store.RootID, "bericht.txt", data, true)
	e.waitReady(id)
	var share struct{ Token string }
	json.Unmarshal(e.must("POST", "/api/shares", map[string]any{"nodeId": id, "password": "alt", "expiresMinutes": 5}), &share)

	visitorJar, _ := cookiejar.New(nil)
	visitor := &env{t: t, srv: e.srv, client: &http.Client{Jar: visitorJar, CheckRedirect: e.client.CheckRedirect}}
	form := []byte("password=alt")
	if res, _ := visitor.do("POST", "/s/"+share.Token, form, "Content-Type", "application/x-www-form-urlencoded"); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("unlock: %d", res.StatusCode)
	}
	if res, _ := visitor.do("GET", "/s/"+share.Token+"/dl", nil); res.StatusCode != 200 {
		t.Fatalf("unlocked download: %d", res.StatusCode)
	}

	// A changed password locks out whoever knew the old one.
	e.must("PATCH", "/api/shares/"+share.Token, map[string]any{"password": "neu"})
	if res, _ := visitor.do("GET", "/s/"+share.Token+"/dl", nil); res.StatusCode != http.StatusSeeOther {
		t.Fatalf("old unlock still works: %d", res.StatusCode)
	}

	listed := func(token string) (found struct {
		Token       string
		HasPassword bool
		ExpiresAt   int64
	}) {
		var list []struct {
			Token       string
			HasPassword bool
			ExpiresAt   int64
		}
		json.Unmarshal(e.must("GET", "/api/shares", nil), &list)
		for _, sh := range list {
			if sh.Token == token {
				found = sh
			}
		}
		return found
	}

	// A new lifetime counts from now, untouched fields stay.
	e.must("PATCH", "/api/shares/"+share.Token, map[string]any{"expiresMinutes": 60})
	if got := listed(share.Token); !got.HasPassword || got.ExpiresAt < time.Now().Add(59*time.Minute).Unix() {
		t.Fatalf("after new lifetime: %+v", got)
	}
	e.must("PATCH", "/api/shares/"+share.Token, map[string]any{"expiresMinutes": 0, "password": ""})
	if got := listed(share.Token); got.HasPassword || got.ExpiresAt != 0 {
		t.Fatalf("after removing: %+v", got)
	}

	var moved struct{ Token string }
	json.Unmarshal(e.must("PATCH", "/api/shares/"+share.Token, map[string]any{"newLink": true}), &moved)
	if moved.Token == "" || moved.Token == share.Token {
		t.Fatalf("new link: %q", moved.Token)
	}
	if res, _ := visitor.do("GET", "/s/"+share.Token, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("old link: %d", res.StatusCode)
	}
	if res, got := visitor.do("GET", "/s/"+moved.Token, nil); res.StatusCode != 200 || !bytes.Equal(got, data) {
		t.Fatalf("new link: %d", res.StatusCode)
	}

	if res, _ := e.do("PATCH", "/api/shares/"+moved.Token, map[string]any{"expiresMinutes": maxShareMinutes + 1}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("too long: %d", res.StatusCode)
	}
	if res, _ := e.do("PATCH", "/api/shares/gibtsnicht", map[string]any{"newLink": true}); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown link: %d", res.StatusCode)
	}
}

func TestDirectShare(t *testing.T) {
	e := newEnv(t)
	e.setup()
	png := []byte("\x89PNG\r\n\x1a\nbild")
	id := e.upload(store.RootID, "bild.png", png, true)
	zip := e.upload(store.RootID, "paket.bin", []byte("daten"), true)
	folder, _ := e.engine.Store.Mkdir(store.RootID, "ordner")
	e.waitReady(id)
	e.waitReady(zip)

	share := func(body map[string]any) string {
		var out struct{ Token string }
		json.Unmarshal(e.must("POST", "/api/shares", body), &out)
		return "/s/" + out.Token
	}
	visitorJar, _ := cookiejar.New(nil)
	visitor := &env{t: t, srv: e.srv, client: &http.Client{Jar: visitorJar, CheckRedirect: e.client.CheckRedirect}}

	res, got := visitor.do("GET", share(map[string]any{"nodeId": id}), nil)
	if res.StatusCode != 200 || !bytes.Equal(got, png) || res.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("image: %d %v", res.StatusCode, res.Header)
	}
	if res, _ := visitor.do("GET", share(map[string]any{"nodeId": zip}), nil); !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("other file: %v", res.Header)
	}
	if _, body := visitor.do("GET", share(map[string]any{"nodeId": folder.ID}), nil); !strings.Contains(string(body), "ZIP") {
		t.Fatal("folders keep their page")
	}
	limited := share(map[string]any{"nodeId": id, "maxDownloads": 1})
	if _, body := visitor.do("GET", limited, nil); !strings.Contains(string(body), "Herunterladen") {
		t.Fatal("limited links keep their page")
	}
	locked := share(map[string]any{"nodeId": id, "password": "geheim"})
	if _, body := visitor.do("GET", locked, nil); !strings.Contains(string(body), "Kennwort") {
		t.Fatal("password comes first")
	}
	form := []byte("password=geheim")
	visitor.do("POST", locked, form, "Content-Type", "application/x-www-form-urlencoded")
	if res, got := visitor.do("GET", locked, nil); res.StatusCode != 200 || !bytes.Equal(got, png) {
		t.Fatalf("after password: %d", res.StatusCode)
	}
}

func TestUnfinishedUploadsAreListed(t *testing.T) {
	e := newEnv(t)
	e.setup()
	var created struct{ ID string }
	json.Unmarshal(e.must("POST", "/api/uploads", map[string]any{"parentId": store.RootID, "name": "gross.bin", "size": 10, "encrypted": true, "mtime": 1234}), &created)
	e.must("PATCH", "/api/uploads/"+created.ID, []byte("1234"), "X-Upload-Offset", "0")

	var list []struct {
		ID       string
		Name     string
		Size     int64
		Received int64
		MTime    int64
	}
	json.Unmarshal(e.must("GET", fmt.Sprintf("/api/nodes/%d/uploads", store.RootID), nil), &list)
	if len(list) != 1 || list[0].ID != created.ID || list[0].Received != 4 || list[0].Size != 10 || list[0].MTime != 1234 {
		t.Fatalf("list: %+v", list)
	}
	e.must("DELETE", "/api/uploads/"+created.ID, nil)
	list = nil
	json.Unmarshal(e.must("GET", fmt.Sprintf("/api/nodes/%d/uploads", store.RootID), nil), &list)
	if len(list) != 0 {
		t.Fatalf("after cancel: %+v", list)
	}
}

func TestEndShareStatsAndLog(t *testing.T) {
	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "a.txt", []byte("inhalt"), true)
	e.waitReady(id)

	var share struct{ Token string }
	json.Unmarshal(e.must("POST", "/api/shares", map[string]any{"nodeId": id}), &share)
	var list []store.Node
	json.Unmarshal(e.must("GET", "/api/nodes/1/children", nil), &list)
	if len(list) != 1 || !list[0].Shared || list[0].MD5 == "" || list[0].Segments != 1 {
		t.Fatalf("listing: %+v", list)
	}

	e.must("DELETE", "/api/shares/"+share.Token, nil)
	if res, _ := e.do("GET", "/s/"+share.Token, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("ended share: %d", res.StatusCode)
	}
	json.Unmarshal(e.must("POST", "/api/shares", map[string]any{"nodeId": id}), &share)
	e.must("DELETE", fmt.Sprintf("/api/nodes/%d/shares", id), nil)
	if res, _ := e.do("GET", "/s/"+share.Token, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("ended by node: %d", res.StatusCode)
	}

	var stats store.Stats
	json.Unmarshal(e.must("GET", "/api/stats", nil), &stats)
	if stats.Files != 1 || stats.Segments != 1 || stats.Size != 6 || stats.LastUpload == 0 || stats.Shares != 0 {
		t.Fatalf("stats: %+v", stats)
	}

	var events []store.Event
	json.Unmarshal(e.must("GET", "/api/logs?kind=share", nil), &events)
	if len(events) != 4 {
		t.Fatalf("events: %+v", events)
	}
}

func TestFolderVerify(t *testing.T) {
	e := newEnv(t)
	e.setup()
	folder, _ := e.engine.Store.Mkdir(store.RootID, "ordner")
	good := e.upload(folder.ID, "gut.bin", make([]byte, 3000), true)
	bad := e.upload(folder.ID, "schlecht.bin", make([]byte, 3000), true)
	e.waitReady(good)
	e.waitReady(bad)

	blocks, _ := e.engine.Store.Blocks(bad)
	backend := e.engine.Backend.(*files.LocalBackend)
	path := filepath.Join(backend.Dir, fmt.Sprintf("%d.blob", blocks[0].MsgID))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[10] ^= 1
	os.WriteFile(path, raw, 0o600)

	e.must("POST", "/api/verify", map[string]any{"ids": []int64{folder.ID}, "deep": true})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var st files.VerifyStatus
		json.Unmarshal(e.must("GET", "/api/verify", nil), &st)
		if !st.Running {
			if st.Done != 2 || len(st.Bad) != 1 || st.Bad[0] != "schlecht.bin" {
				t.Fatalf("status: %+v", st)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("verify did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n, _ := e.engine.Store.Get(good); n.VerifyStatus != store.VerifyOK {
		t.Fatalf("good: %+v", n.VerifyStatus)
	}
	if n, _ := e.engine.Store.Get(bad); n.VerifyStatus != store.VerifyBad {
		t.Fatalf("bad: %+v", n.VerifyStatus)
	}
}

func TestQuickVerify(t *testing.T) {
	e := newEnv(t)
	e.setup()
	here := e.upload(store.RootID, "da.bin", make([]byte, 3000), true)
	gone := e.upload(store.RootID, "weg.bin", make([]byte, 3000), true)
	damaged := e.upload(store.RootID, "kaputt.bin", make([]byte, 3000), true)
	e.waitReady(here)
	e.waitReady(gone)
	e.waitReady(damaged)
	ctx := context.Background()

	blocks, _ := e.engine.Store.Blocks(gone)
	if err := e.engine.Backend.Delete(ctx, []int{blocks[0].MsgID}); err != nil {
		t.Fatal(err)
	}
	e.engine.Store.SetVerify(damaged, store.VerifyBad, "Prüfsumme", "")

	for _, id := range []int64{here, gone, damaged} {
		n, _ := e.engine.Store.Get(id)
		e.engine.Present(ctx, n)
	}
	if n, _ := e.engine.Store.Get(here); n.VerifyStatus != store.VerifyPresent {
		t.Fatalf("here: %+v", n.VerifyStatus)
	}
	if n, _ := e.engine.Store.Get(gone); n.VerifyStatus != store.VerifyBad {
		t.Fatalf("gone: %+v", n.VerifyStatus)
	}
	if n, _ := e.engine.Store.Get(damaged); n.VerifyStatus != store.VerifyBad || n.VerifyNote != "Prüfsumme" {
		t.Fatalf("damaged: %+v %q", n.VerifyStatus, n.VerifyNote)
	}
}

func TestPurgeDeletesFromBackendAtOnce(t *testing.T) {
	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "weg.bin", make([]byte, 100), true)
	e.waitReady(id)
	backend := e.engine.Backend.(*files.LocalBackend)
	e.must("POST", "/api/delete", map[string]any{"ids": []int64{id}})
	if entries, _ := os.ReadDir(backend.Dir); len(entries) == 0 {
		t.Fatal("trash must keep the data")
	}
	e.must("POST", "/api/trash/purge", map[string]any{"ids": []int64{id}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		entries, _ := os.ReadDir(backend.Dir)
		remaining := 0
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".blob") {
				remaining++
			}
		}
		// The startup backup may hold one blob of its own.
		if remaining <= 1 {
			if orphans, _ := e.engine.Store.Orphans(10); len(orphans) == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d blobs left", remaining)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMigrationKeepsOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE nodes (id INTEGER PRIMARY KEY, parent_id INTEGER, name TEXT NOT NULL, is_dir INTEGER NOT NULL DEFAULT 0,
		size INTEGER NOT NULL DEFAULT 0, mime TEXT NOT NULL DEFAULT '', encrypted INTEGER NOT NULL DEFAULT 0, policy INTEGER NOT NULL DEFAULT 0,
		state TEXT NOT NULL DEFAULT 'ready', error TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL DEFAULT '', wrapped_key BLOB, stage_path TEXT NOT NULL DEFAULT '',
		attempts INTEGER NOT NULL DEFAULT 0, next_try INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL, ctime INTEGER NOT NULL, trashed_at INTEGER);
		INSERT INTO nodes(id, parent_id, name, is_dir, mtime, ctime) VALUES (1, NULL, '', 1, 1, 1), (2, 1, 'alt.txt', 0, 1, 1);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if n, err := st.Child(store.RootID, "alt.txt"); err != nil || n.MD5 != "" {
		t.Fatalf("%+v %v", n, err)
	}
}

func TestNameValidation(t *testing.T) {
	e := newEnv(t)
	e.setup()
	for _, name := range []string{"..", "a/b", `a\b`, ""} {
		if res, _ := e.do("POST", "/api/nodes/1/mkdir", map[string]string{"name": name}); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("name %q: %d", name, res.StatusCode)
		}
	}
}

func TestSFTP(t *testing.T) {
	e := newEnv(t)
	e.setup()
	hash, _ := auth.HashPassword("sftp-kennwort")
	user, _ := e.auth.User()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &sftpd.Server{Addr: addr, Engine: e.engine, Auth: e.auth}
	go server.Run(ctx)

	dial := func(password string) (*ssh.Client, error) {
		var conn *ssh.Client
		var err error
		for range 50 {
			conn, err = ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "martin", Auth: []ssh.AuthMethod{ssh.Password(password)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
			var opErr *net.OpError
			if !errors.As(err, &opErr) {
				return conn, err
			}
			time.Sleep(20 * time.Millisecond)
		}
		return conn, err
	}

	if _, err := dial("sftp-kennwort"); err == nil {
		t.Fatal("SFTP must stay closed until a password is set")
	}
	user.SFTP = hash
	e.auth.Save(user)
	e.auth.Limiter.Success("sftp:127.0.0.1")
	server.Sync()

	conn, err := dial("sftp-kennwort")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	defer func() {
		user.SFTP = ""
		e.auth.Save(user)
		if err := server.Sync(); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ReadDir("/"); err == nil {
			t.Fatal("switching SFTP off must end running sessions")
		}
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			t.Fatal("switching SFTP off must close the port")
		}
	}()

	if err := client.Mkdir("/ordner"); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5<<20)
	rand.Read(data)
	w, err := client.Create("/ordner/datei.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	folder, err := e.engine.Store.Child(store.RootID, "ordner")
	if err != nil {
		t.Fatal(err)
	}
	node, err := e.engine.Store.Child(folder.ID, "datei.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !node.Encrypted {
		t.Fatal("SFTP uploads must follow the encrypted default")
	}
	e.waitReady(node.ID)

	r, err := client.Open("/ordner/datei.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back: %v, %d bytes", err, len(got))
	}
	infos, err := client.ReadDir("/ordner")
	if err != nil || len(infos) != 1 || infos[0].Size() != int64(len(data)) {
		t.Fatalf("readdir: %v %v", infos, err)
	}
	if err := client.Rename("/ordner/datei.bin", "/umbenannt.bin"); err != nil {
		t.Fatal(err)
	}
	if err := client.Remove("/umbenannt.bin"); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveDirectory("/ordner"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Stat("/ordner"); err == nil {
		t.Fatal("folder still exists")
	}

	// A folder can opt out of encryption, for SFTP as well.
	plain, _ := e.engine.Store.Mkdir(store.RootID, "offen")
	e.engine.Store.SetPolicy(plain.ID, store.PolicyPlain)
	w, _ = client.Create("/offen/klar.txt")
	w.Write([]byte("hallo"))
	w.Close()
	n, err := e.engine.Store.Child(plain.ID, "klar.txt")
	if err != nil || n.Encrypted {
		t.Fatalf("plain folder: %+v %v", n, err)
	}
}
