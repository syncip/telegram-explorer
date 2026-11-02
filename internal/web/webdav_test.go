package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/davd"
	"telegram-explorer/internal/store"
)

func TestWebDAV(t *testing.T) {
	e := newEnv(t)
	e.setup()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := &davd.Server{Addr: addr, Hosts: "127.0.0.1", Engine: e.engine, Auth: e.auth}
	go server.Run(ctx)

	// The client trusts exactly the certificate the settings hand out.
	cert, err := server.Cert()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(cert.PEM)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	base := "https://" + addr
	do := func(method, path, password string, body []byte, headers ...string) (*http.Response, []byte) {
		t.Helper()
		var res *http.Response
		for range 50 {
			req, _ := http.NewRequest(method, base+path, bytes.NewReader(body))
			if password != "" {
				req.SetBasicAuth("martin", password)
			}
			for i := 0; i+1 < len(headers); i += 2 {
				req.Header.Set(headers[i], headers[i+1])
			}
			if res, err = client.Do(req); err == nil {
				break
			}
			var opErr *net.OpError
			if !errors.As(err, &opErr) {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if res == nil {
			return nil, nil
		}
		got, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, got
	}

	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatal("WebDAV must stay closed until a password is set")
	}
	user, _ := e.auth.User()
	user.WebDAV, _ = auth.HashPassword("dav-kennwort")
	e.auth.Save(user)
	if err := server.Sync(); err != nil {
		t.Fatal(err)
	}

	if res, _ := do("PROPFIND", "/", "", nil, "Depth", "1"); res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("without password: %d", res.StatusCode)
	}
	if res, _ := do("PROPFIND", "/", "falsch", nil, "Depth", "1"); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", res.StatusCode)
	}
	e.auth.Limiter.Success("webdav:127.0.0.1")

	if res, _ := do("MKCOL", "/ordner", "dav-kennwort", nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("mkcol: %d", res.StatusCode)
	}
	data := make([]byte, 3<<20)
	rand.Read(data)
	if res, body := do("PUT", "/ordner/datei.bin", "dav-kennwort", data); res.StatusCode != http.StatusCreated {
		t.Fatalf("put: %d %s", res.StatusCode, body)
	}
	folder, _ := e.engine.Store.Child(store.RootID, "ordner")
	node, err := e.engine.Store.Child(folder.ID, "datei.bin")
	if err != nil || !node.Encrypted || node.Size != int64(len(data)) {
		t.Fatalf("stored: %+v %v", node, err)
	}
	e.waitReady(node.ID)

	if res, got := do("GET", "/ordner/datei.bin", "dav-kennwort", nil); res.StatusCode != 200 || !bytes.Equal(got, data) {
		t.Fatalf("get: %d, %d bytes", res.StatusCode, len(got))
	}
	if res, got := do("GET", "/ordner/datei.bin", "dav-kennwort", nil, "Range", "bytes=1048570-1048579"); res.StatusCode != http.StatusPartialContent || !bytes.Equal(got, data[1048570:1048580]) {
		t.Fatalf("range: %d %d", res.StatusCode, len(got))
	}
	if res, body := do("PROPFIND", "/ordner", "dav-kennwort", nil, "Depth", "1"); res.StatusCode != 207 || !strings.Contains(string(body), "datei.bin") || !strings.Contains(string(body), "application/octet-stream") {
		t.Fatalf("propfind: %d %s", res.StatusCode, body)
	}
	if res, _ := do("MOVE", "/ordner/datei.bin", "dav-kennwort", nil, "Destination", base+"/umbenannt.bin"); res.StatusCode != http.StatusCreated {
		t.Fatalf("move: %d", res.StatusCode)
	}
	// Overwriting keeps the old version in the trash, like everywhere else.
	if res, _ := do("PUT", "/umbenannt.bin", "dav-kennwort", []byte("neu")); res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusNoContent {
		t.Fatalf("overwrite: %d", res.StatusCode)
	}
	if res, got := do("GET", "/umbenannt.bin", "dav-kennwort", nil); string(got) != "neu" {
		t.Fatalf("after overwrite: %d %q", res.StatusCode, got)
	}
	if res, _ := do("DELETE", "/ordner", "dav-kennwort", nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res, _ := do("PROPFIND", "/ordner", "dav-kennwort", nil, "Depth", "0"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted folder: %d", res.StatusCode)
	}

	user.WebDAV = ""
	e.auth.Save(user)
	server.Sync()
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Fatal("switching WebDAV off must close the port")
	}
}
