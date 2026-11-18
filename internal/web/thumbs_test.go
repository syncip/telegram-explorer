package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"path/filepath"
	"testing"

	"telegram-explorer/internal/store"
	"telegram-explorer/internal/thumbs"
)

func testJPEG(t *testing.T, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestThumbnails(t *testing.T) {
	e := newEnv(t)
	e.server.Thumbs = &thumbs.Maker{Dir: filepath.Join(t.TempDir(), "thumbs"), Vault: e.engine.Vault,
		Open: func(ctx context.Context, n *store.Node) (thumbs.Source, error) { return e.engine.Open(ctx, n) }}
	e.setup()
	photo := e.upload(store.RootID, "foto.jpg", testJPEG(t, 1600, 900), true)
	text := e.upload(store.RootID, "notiz.txt", []byte("hallo"), false)
	e.waitReady(photo)

	var list []struct {
		ID    int64
		Thumb bool
	}
	json.Unmarshal(e.must("GET", "/api/nodes/1/children", nil), &list)
	for _, n := range list {
		if n.Thumb != (n.ID == photo) {
			t.Fatalf("thumb flags: %+v", list)
		}
	}
	res, data := e.do("GET", fmt.Sprintf("/api/nodes/%d/thumb", photo), nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb: %d %s", res.StatusCode, data)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != thumbs.Size || cfg.Height != 270 {
		t.Fatalf("thumb size %v %v", cfg, err)
	}
	if res, _ := e.do("GET", fmt.Sprintf("/api/nodes/%d/thumb", text), nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("text thumb: %d", res.StatusCode)
	}

	// The cached preview of an encrypted photo is encrypted too.
	matches, _ := filepath.Glob(filepath.Join(e.server.Thumbs.Dir, fmt.Sprint(photo)+"-*.jpg"))
	if len(matches) != 1 {
		t.Fatalf("cache: %v", matches)
	}
	e.engine.SetVault(nil)
	if res, _ := e.do("GET", fmt.Sprintf("/api/nodes/%d/thumb", photo), nil); res.StatusCode != http.StatusLocked {
		t.Fatalf("locked: %d", res.StatusCode)
	}
}
