package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"telegram-explorer/internal/store"
)

func TestEditorSave(t *testing.T) {
	e := newEnv(t)
	e.setup()
	id := e.upload(store.RootID, "notiz.md", []byte("# Alt\n"), true)
	e.waitReady(id)
	var share struct{ Token string }
	json.Unmarshal(e.must("POST", "/api/shares", map[string]any{"nodeId": id}), &share)

	var saved struct {
		ID        int64
		Name      string
		Size      int64
		Encrypted bool
	}
	json.Unmarshal(e.must("PUT", fmt.Sprintf("/api/nodes/%d/content", id), []byte("# Neu\nmit Umlauten: äöü\n")), &saved)
	if saved.ID == id || saved.Name != "notiz.md" || !saved.Encrypted || saved.Size != int64(len("# Neu\nmit Umlauten: äöü\n")) {
		t.Fatalf("saved: %+v", saved)
	}
	e.waitReady(saved.ID)
	if got := string(e.must("GET", fmt.Sprintf("/api/nodes/%d/content", saved.ID), nil)); !strings.HasPrefix(got, "# Neu") {
		t.Fatalf("content: %q", got)
	}

	// The old version is in the trash and the link shows the new one.
	old, _ := e.engine.Store.Get(id)
	if old.TrashedAt == 0 {
		t.Fatal("old version not in trash")
	}
	if got := string(e.must("GET", "/s/"+share.Token+"/dl", nil)); !strings.HasPrefix(got, "# Neu") {
		t.Fatalf("share: %q", got)
	}

	// Saving the outdated version again is refused and names the current file.
	res, data := e.do("PUT", fmt.Sprintf("/api/nodes/%d/content", id), []byte("veraltet"))
	var conflict struct {
		Error   string
		Current int64
	}
	json.Unmarshal(data, &conflict)
	if res.StatusCode != http.StatusConflict || conflict.Current != saved.ID {
		t.Fatalf("conflict: %d %s", res.StatusCode, data)
	}
	// Forced, it replaces the current file.
	var forced struct{ ID int64 }
	json.Unmarshal(e.must("PUT", fmt.Sprintf("/api/nodes/%d/content?force=1", id), []byte("trotzdem")), &forced)
	e.waitReady(forced.ID)
	if got := string(e.must("GET", fmt.Sprintf("/api/nodes/%d/content", forced.ID), nil)); got != "trotzdem" {
		t.Fatalf("forced: %q", got)
	}
	if cur, _ := e.engine.Store.Get(saved.ID); cur.TrashedAt == 0 {
		t.Fatal("replaced version not in trash")
	}
	if got := string(e.must("GET", "/s/"+share.Token+"/dl", nil)); got != "trotzdem" {
		t.Fatalf("share after force: %q", got)
	}

	// Too large for the editor.
	big := make([]byte, maxEdit+1)
	if res, _ := e.do("PUT", fmt.Sprintf("/api/nodes/%d/content", forced.ID), big); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("big: %d", res.StatusCode)
	}
	// Folders cannot be saved.
	if res, _ := e.do("PUT", "/api/nodes/1/content", []byte("x")); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("folder: %d", res.StatusCode)
	}
}

func TestNewTextFile(t *testing.T) {
	e := newEnv(t)
	e.setup()
	var created struct {
		ID   int64
		Name string
		Size int64
	}
	json.Unmarshal(e.must("POST", "/api/nodes/1/textfile", map[string]string{"name": "Neu.txt"}), &created)
	if created.Name != "Neu.txt" || created.Size != 0 {
		t.Fatalf("created: %+v", created)
	}
	if got := e.must("GET", fmt.Sprintf("/api/nodes/%d/content", created.ID), nil); len(got) != 0 {
		t.Fatalf("not empty: %q", got)
	}
	if res, _ := e.do("POST", "/api/nodes/1/textfile", map[string]string{"name": "Neu.txt"}); res.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}
	var saved struct{ ID int64 }
	json.Unmarshal(e.must("PUT", fmt.Sprintf("/api/nodes/%d/content", created.ID), []byte("erste Zeile")), &saved)
	e.waitReady(saved.ID)
	if got := string(e.must("GET", fmt.Sprintf("/api/nodes/%d/content", saved.ID), nil)); got != "erste Zeile" {
		t.Fatalf("content: %q", got)
	}
}
