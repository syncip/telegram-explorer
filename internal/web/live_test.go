package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"telegram-explorer/internal/store"
)

func cs(t *testing.T, raw string) changeSet {
	t.Helper()
	c, err := parseChanges(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChangeSet(t *testing.T) {
	doc := utf16.Encode([]rune("Hallo 👋\nWelt"))
	// The emoji counts two, as in JavaScript.
	c := cs(t, `[6, [2, "🌍", "neu"], 5]`)
	out, err := c.apply(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(utf16.Decode(out)); got != "Hallo 🌍\nneu\nWelt" {
		t.Fatalf("apply: %q", got)
	}
	if _, err := cs(t, `[3]`).apply(doc); err == nil {
		t.Fatal("wrong length accepted")
	}
	for _, bad := range []string{`{}`, `[[-1]]`, `["x"]`, `[[1, 2]]`, `[[0, "a\nb"]]`} {
		if _, err := parseChanges(json.RawMessage(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	// Positions before, at, inside and after the replaced section.
	for pos, want := range map[int]int{0: 0, 6: 6, 7: 6, 8: 12, 13: 17} {
		if got := c.mapPos(pos); got != want {
			t.Errorf("mapPos(%d) = %d, want %d", pos, got, want)
		}
	}
	// An insertion right at a cursor stays behind it.
	if got := cs(t, `[2, [0, "ab"], 11]`).mapPos(2); got != 2 {
		t.Errorf("insert at cursor: %d", got)
	}

	text, bom, crlf, err := textOf([]byte("\xEF\xBB\xBFa\r\nb\rc"))
	if err != nil || !bom || !crlf || string(utf16.Decode(text)) != "a\nb\nc" {
		t.Fatalf("textOf: %q %v %v %v", string(utf16.Decode(text)), bom, crlf, err)
	}
	if got := string(fileOf(text, bom, crlf)); got != "\xEF\xBB\xBFa\r\nb\r\nc" {
		t.Fatalf("fileOf: %q", got)
	}
	if _, _, _, err := textOf([]byte{0xff, 0xfe}); err == nil {
		t.Fatal("binary accepted")
	}
}

type liveJoined struct {
	Epoch   string
	Version int
	Doc     string
}

type livePulled struct {
	Updates []struct {
		ClientID string
		Changes  json.RawMessage
	}
	Version int
	State   struct {
		Rev   int
		Peers []struct {
			ID, Name   string
			Anchor     int
			Head       int
		}
		Pending bool
		Problem string
		Gone    bool
		Node    struct{ ID int64 }
	}
}

func (e *env) liveJoin(id int64, client string) liveJoined {
	e.t.Helper()
	var j liveJoined
	json.Unmarshal(e.must("POST", fmt.Sprintf("/api/live/%d/join", id), map[string]string{"clientID": client, "name": client}), &j)
	return j
}

func (e *env) livePush(id int64, epoch, client string, version int, changes ...string) bool {
	e.t.Helper()
	ups := make([]json.RawMessage, len(changes))
	for i, c := range changes {
		ups[i] = json.RawMessage(c)
	}
	var out struct{ OK bool }
	json.Unmarshal(e.must("POST", fmt.Sprintf("/api/live/%d/push", id), map[string]any{"epoch": epoch, "clientID": client, "version": version, "updates": ups}), &out)
	return out.OK
}

func (e *env) livePull(id int64, epoch, client string, version, rev int) livePulled {
	e.t.Helper()
	var p livePulled
	json.Unmarshal(e.must("GET", fmt.Sprintf("/api/live/%d/pull?epoch=%s&clientID=%s&version=%d&rev=%d", id, epoch, client, version, rev), nil), &p)
	return p
}

func (e *env) liveText(name string) (int64, string) {
	e.t.Helper()
	var list []struct {
		ID   int64
		Name string
	}
	json.Unmarshal(e.must("GET", "/api/nodes/1/children", nil), &list)
	for _, n := range list {
		if n.Name == name {
			e.waitReady(n.ID)
			return n.ID, string(e.must("GET", fmt.Sprintf("/api/nodes/%d/content", n.ID), nil))
		}
	}
	e.t.Fatalf("%s not found", name)
	return 0, ""
}

func TestLiveSession(t *testing.T) {
	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "notiz.md", []byte("\xEF\xBB\xBFHallo\r\nWelt"), true)
	e.waitReady(id)
	var share struct{ Token string }
	json.Unmarshal(e.must("POST", "/api/shares", map[string]any{"nodeId": id}), &share)

	a := e.liveJoin(id, "A")
	b := e.liveJoin(id, "B")
	if a.Epoch != b.Epoch || a.Doc != "Hallo\nWelt" || b.Version != 0 {
		t.Fatalf("join: %+v %+v", a, b)
	}

	// B's cursor at the end of "Welt", sent for version 0.
	e.must("POST", fmt.Sprintf("/api/live/%d/presence", id), map[string]any{"epoch": a.Epoch, "clientID": "B", "name": "Handy", "version": 0, "anchor": 10, "head": 10})

	// A long poll wakes up when A pushes.
	woke := make(chan livePulled, 1)
	go func() { woke <- e.livePull(id, b.Epoch, "B", 0, -1) }()
	time.Sleep(50 * time.Millisecond)
	if !e.livePush(id, a.Epoch, "A", 0, `[5, [0, " du"], 5]`) {
		t.Fatal("push A refused")
	}
	select {
	case <-woke:
	case <-time.After(5 * time.Second):
		t.Fatal("pull did not return")
	}

	// B, still on version 0, is refused and pulls first.
	if e.livePush(id, b.Epoch, "B", 0, `[10, [0, "!"]]`) {
		t.Fatal("stale push accepted")
	}
	p := e.livePull(id, b.Epoch, "B", 0, -1)
	if p.Version != 1 || len(p.Updates) != 1 || p.Updates[0].ClientID != "A" {
		t.Fatalf("pull: %+v", p)
	}
	if !e.livePush(id, b.Epoch, "B", 1, `[13, [0, "!"]]`) {
		t.Fatal("rebased push refused")
	}

	// A sees B's cursor moved by A's own insertion.
	p = e.livePull(id, a.Epoch, "A", 2, -1)
	if len(p.State.Peers) != 1 || p.State.Peers[0].Name != "Handy" || p.State.Peers[0].Head != 13 || !p.State.Pending {
		t.Fatalf("peers: %+v", p.State)
	}

	// The last one out stores the file, with BOM and Windows line breaks.
	e.must("POST", fmt.Sprintf("/api/live/%d/leave", id), map[string]string{"epoch": a.Epoch, "clientID": "A"})
	if _, got := e.liveText("notiz.md"); got != "\xEF\xBB\xBFHallo\r\nWelt" {
		t.Fatalf("stored while B still edits: %q", got)
	}
	e.must("POST", fmt.Sprintf("/api/live/%d/leave", id), map[string]string{"epoch": a.Epoch, "clientID": "B"})
	newID, got := e.liveText("notiz.md")
	if got != "\xEF\xBB\xBFHallo du\r\nWelt!" || newID == id {
		t.Fatalf("stored: %d %q", newID, got)
	}
	if old, _ := e.engine.Store.Get(id); old.TrashedAt == 0 {
		t.Fatal("old version not in trash")
	}
	if got := string(e.must("GET", "/s/"+share.Token+"/dl", nil)); got != "\xEF\xBB\xBFHallo du\r\nWelt!" {
		t.Fatalf("share: %q", got)
	}

	// The session is over; its epoch is no longer known.
	if res, _ := e.do("POST", fmt.Sprintf("/api/live/%d/push", newID), map[string]any{"epoch": a.Epoch, "clientID": "A", "version": 2, "updates": []string{}}); res.StatusCode != 410 {
		t.Fatalf("old session: %d", res.StatusCode)
	}
	// Opening the old version continues with the current file.
	if old := e.liveJoin(id, "D"); old.Doc != "Hallo du\nWelt!" {
		t.Fatalf("old id: %+v", old)
	}
	// Broken changes are refused.
	c := e.liveJoin(newID, "C")
	if res, _ := e.do("POST", fmt.Sprintf("/api/live/%d/push", newID), map[string]any{"epoch": c.Epoch, "clientID": "C", "version": 0, "updates": []json.RawMessage{json.RawMessage(`[3]`)}}); res.StatusCode != 400 {
		t.Fatalf("bad change: %d", res.StatusCode)
	}
}

func TestLiveIdleSaveAndDraft(t *testing.T) {
	defer func(a, b, c time.Duration) { liveSaveIdle, liveDraftDelay, liveTick = a, b, c }(liveSaveIdle, liveDraftDelay, liveTick)
	liveSaveIdle, liveDraftDelay, liveTick = 300*time.Millisecond, 10*time.Millisecond, 20*time.Millisecond

	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "liste.txt", []byte("a"), false)
	e.waitReady(id)
	j := e.liveJoin(id, "A")
	e.livePush(id, j.Epoch, "A", 0, `[1, [0, "b"]]`)

	// A draft appears first, then the file is stored while A keeps editing.
	draftFile := filepath.Join(e.server.draftDir(), j.Epoch+".json")
	deadline := time.Now().Add(5 * time.Second)
	sawDraft := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(draftFile); err == nil {
			sawDraft = true
		}
		if p := e.livePull(id, j.Epoch, "A", 1, -1); !p.State.Pending && p.State.Node.ID != id {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawDraft {
		t.Fatal("no draft written")
	}
	if _, got := e.liveText("liste.txt"); got != "ab" {
		t.Fatalf("idle save: %q", got)
	}
	if _, err := os.Stat(draftFile); !os.IsNotExist(err) {
		t.Fatal("draft not removed after save")
	}

	// A draft left by a crash is stored when the vault opens.
	data, _ := json.Marshal(draft{Node: 999, Parent: store.RootID, Name: "gerettet.md", Content: []byte("# Entwurf")})
	os.MkdirAll(e.server.draftDir(), 0o700)
	os.WriteFile(filepath.Join(e.server.draftDir(), "x.json"), data, 0o600)
	e.server.liveRecover()
	if _, got := e.liveText("gerettet.md"); got != "# Entwurf" {
		t.Fatalf("recovered: %q", got)
	}
}

func TestLiveDeletedWhileOpen(t *testing.T) {
	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "weg.txt", []byte("x"), false)
	e.waitReady(id)
	j := e.liveJoin(id, "A")
	e.livePush(id, j.Epoch, "A", 0, `[1, [0, "y"]]`)
	e.must("POST", "/api/delete", map[string]any{"ids": []int64{id}})
	e.server.FlushLive()
	p := e.livePull(id, j.Epoch, "A", 1, -1)
	if !p.State.Gone || p.State.Problem == "" {
		t.Fatalf("state: %+v", p.State)
	}
	// The file stays deleted, the text waits in a draft.
	if n, _ := e.engine.Store.Child(store.RootID, "weg.txt"); n != nil && n.TrashedAt == 0 {
		t.Fatal("deleted file came back")
	}
	if paths, _ := filepath.Glob(filepath.Join(e.server.draftDir(), "*.json")); len(paths) != 1 {
		t.Fatalf("drafts: %v", paths)
	}
}
