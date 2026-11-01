package web

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
)

func (s *Server) getNode(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	chain, err := s.Engine.Store.Path(id)
	if err != nil {
		return err
	}
	encrypt, err := s.Engine.Store.EncryptByDefault(id)
	if err != nil && chain[len(chain)-1].IsDir {
		return err
	}
	return writeJSON(w, map[string]any{"node": chain[len(chain)-1], "path": chain, "encryptDefault": encrypt})
}

func (s *Server) children(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := s.Engine.Store.List(id)
	if err != nil {
		return err
	}
	return writeJSON(w, list)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		return writeJSON(w, []*store.Node{})
	}
	list, err := s.Engine.Store.Search(q)
	if err != nil {
		return err
	}
	return writeJSON(w, list)
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := files.ValidName(in.Name); err != nil {
		return badRequest(err.Error())
	}
	n, err := s.Engine.Store.Mkdir(id, in.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, n)
}

func (s *Server) patchNode(w http.ResponseWriter, r *http.Request) error {
	n, err := s.liveNode(r)
	if err != nil {
		return err
	}
	var in struct {
		Name   *string
		Policy *int
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.Name != nil {
		if err := files.ValidName(*in.Name); err != nil {
			return badRequest(err.Error())
		}
		if err := s.Engine.Store.Move(n.ID, n.ParentID, *in.Name); err != nil {
			return err
		}
	}
	if in.Policy != nil {
		if *in.Policy < store.PolicyInherit || *in.Policy > store.PolicyPlain || (n.ID == store.RootID && *in.Policy == store.PolicyInherit) {
			return badRequest("ungültige Einstellung")
		}
		if err := s.Engine.Store.SetPolicy(n.ID, *in.Policy); err != nil {
			return err
		}
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) move(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs    []int64
		Target int64
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	for _, id := range in.IDs {
		n, err := s.Engine.Store.Get(id)
		if err != nil {
			return err
		}
		if err := s.Engine.Store.Move(id, in.Target, n.Name); err != nil {
			return fmt.Errorf("%s: %w", n.Name, err)
		}
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) deleteNodes(w http.ResponseWriter, r *http.Request) error {
	var in struct{ IDs []int64 }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	for _, id := range in.IDs {
		if err := s.Engine.Delete(id); err != nil {
			return err
		}
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) trash(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Engine.Store.ListTrash()
	if err != nil {
		return err
	}
	return writeJSON(w, list)
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) error {
	var in struct{ IDs []int64 }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	for _, id := range in.IDs {
		if err := s.Engine.Store.Restore(id); err != nil {
			return err
		}
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) purge(w http.ResponseWriter, r *http.Request) error {
	var in struct{ IDs []int64 }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	for _, id := range in.IDs {
		n, err := s.Engine.Store.Get(id)
		if err != nil {
			return err
		}
		if n.TrashedAt == 0 {
			return badRequest("nur Einträge aus dem Papierkorb können endgültig gelöscht werden")
		}
		if err := s.Engine.Purge(id); err != nil {
			return err
		}
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) startVerify(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs  []int64
		Deep bool
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	added, err := s.Engine.EnqueueVerify(in.IDs, in.Deep)
	if err != nil {
		return err
	}
	return writeJSON(w, map[string]int{"added": added})
}

func (s *Server) verifyState(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, s.Engine.VerifyState())
}

func (s *Server) clearVerify(w http.ResponseWriter, r *http.Request) error {
	s.Engine.ClearVerify()
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Engine.Store.Stats()
	if err != nil {
		return err
	}
	return writeJSON(w, st)
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	list, err := s.Engine.Store.Events(q.Get("level"), q.Get("kind"), 1000)
	if err != nil {
		return err
	}
	return writeJSON(w, list)
}

func (s *Server) clearLogs(w http.ResponseWriter, r *http.Request) error {
	if err := s.Engine.Store.ClearEvents(); err != nil {
		return err
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) endSharesOf(w http.ResponseWriter, r *http.Request) error {
	n, err := s.liveNode(r)
	if err != nil {
		return err
	}
	count, err := s.Engine.Store.DeleteSharesOf(n.ID)
	if err != nil {
		return err
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindShare, "Freigabe für „%s“ beendet (%d Link(s))", n.Name, count)
	return writeJSON(w, map[string]int64{"ended": count})
}

// logDownload records a download once: players and resuming clients send many
// range requests for what is a single download to the person watching the log.
func (s *Server) logDownload(r *http.Request, who, name string) {
	if r.Method != http.MethodGet {
		return
	}
	if rng := r.Header.Get("Range"); rng != "" && !strings.HasPrefix(rng, "bytes=0-") {
		return
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindDownload, "„%s“ %s", name, who)
}

func (s *Server) retry(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := s.Engine.Store.RetryNow(id); err != nil {
		return err
	}
	s.Engine.Wake()
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Engine.Jobs()
	if err != nil {
		return err
	}
	return writeJSON(w, list)
}

// inlineTypes may render on our origin. Everything else, above all HTML and SVG,
// is forced to download, so a stored file cannot run script in the UI's origin.
func inlineAllowed(t string) bool {
	switch {
	case strings.HasPrefix(t, "video/"), strings.HasPrefix(t, "audio/"), t == "application/pdf":
		return true
	case t == "image/png", t == "image/jpeg", t == "image/gif", t == "image/webp":
		return true
	}
	return strings.HasPrefix(t, "text/plain")
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, n *store.Node, inline bool) error {
	reader, err := s.Engine.Open(r.Context(), n)
	if err != nil {
		return err
	}
	defer reader.Close()
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; sandbox")
	switch {
	case n.Mime == "application/pdf":
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	case strings.HasPrefix(n.Mime, "video/"), strings.HasPrefix(n.Mime, "audio/"):
		// The browser's own player does not load inside a sandbox.
		h.Set("Content-Security-Policy", "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'")
	}
	disposition := "attachment"
	if inline && inlineAllowed(n.Mime) {
		disposition = "inline"
		h.Set("Content-Type", n.Mime)
	} else {
		h.Set("Content-Type", "application/octet-stream")
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": n.Name}))
	if n.SHA256 != "" {
		h.Set("ETag", `"`+n.SHA256+`"`)
	}
	http.ServeContent(w, r, "", time.Unix(n.MTime, 0), reader)
	return nil
}

func (s *Server) content(w http.ResponseWriter, r *http.Request) error {
	n, err := s.liveNode(r)
	if err != nil {
		return err
	}
	if n.IsDir {
		return badRequest("ist ein Ordner")
	}
	inline := r.URL.Query().Get("inline") == "1"
	if !inline && r.Header.Get("Range") == "" {
		s.logDownload(r, "heruntergeladen", n.Name)
	}
	return s.serveFile(w, r, n, inline)
}

func (s *Server) zipDownload(w http.ResponseWriter, r *http.Request) error {
	entries := map[string]*store.Node{}
	for _, raw := range strings.Split(r.URL.Query().Get("ids"), ",") {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return badRequest("ungültige ID")
		}
		if err := s.collect(id, entries); err != nil {
			return err
		}
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindDownload, "ZIP mit %d Datei(en) heruntergeladen", len(entries))
	return s.writeZip(w, r, "Telegram Explorer.zip", entries)
}

func (s *Server) collect(id int64, entries map[string]*store.Node) error {
	n, err := s.Engine.Store.Get(id)
	if err != nil {
		return err
	}
	if !n.IsDir {
		entries[n.Name] = n
		return nil
	}
	prefix := n.Name
	if prefix != "" {
		prefix += "/"
	}
	sub, err := s.Engine.Store.Descendants(id, prefix)
	if err != nil {
		return err
	}
	for k, v := range sub {
		entries[k] = v
	}
	return nil
}

// writeZip streams without compression: most large files do not compress, and a
// stream needs no space on the server.
func (s *Server) writeZip(w http.ResponseWriter, r *http.Request, name string, entries map[string]*store.Node) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	zw := zip.NewWriter(w)
	names := make([]string, 0, len(entries))
	for k := range entries {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, rel := range names {
		n := entries[rel]
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Store, Modified: time.Unix(n.MTime, 0)})
		if err != nil {
			return nil
		}
		if err := s.copyNode(r.Context(), fw, n); err != nil {
			if !errors.Is(err, context.Canceled) {
				s.Engine.Store.Log(store.LevelError, store.KindDownload, "ZIP abgebrochen bei „%s“: %v", n.Name, err)
			}
			// Headers are gone; aborting is the only way to signal the failure,
			// and without the central directory the archive is visibly invalid.
			panic(http.ErrAbortHandler)
		}
	}
	return zw.Close()
}

func (s *Server) copyNode(ctx context.Context, w io.Writer, n *store.Node) error {
	reader, err := s.Engine.Open(ctx, n)
	if err != nil {
		return err
	}
	defer reader.Close()
	_, err = io.Copy(w, reader)
	return err
}

type uploadRequest struct {
	ParentID  int64
	Name      string
	Size      int64
	Encrypted *bool
	Overwrite bool
	MTime     int64
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) error {
	var in uploadRequest
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := files.ValidName(in.Name); err != nil {
		return badRequest(err.Error())
	}
	if in.Size < 0 {
		return badRequest("ungültige Größe")
	}
	parent, err := s.Engine.Store.Get(in.ParentID)
	if err != nil {
		return err
	}
	if !parent.IsDir {
		return badRequest("Ziel ist kein Ordner")
	}
	if existing, err := s.Engine.Store.Child(in.ParentID, in.Name); err == nil && (!in.Overwrite || existing.IsDir) {
		return store.ErrExists
	}
	encrypted, err := s.Engine.Store.EncryptByDefault(in.ParentID)
	if err != nil {
		return err
	}
	if in.Encrypted != nil {
		encrypted = *in.Encrypted
	}
	u := &store.Upload{ID: auth.Token(16), ParentID: in.ParentID, Name: in.Name, Size: in.Size,
		Encrypted: encrypted, Overwrite: in.Overwrite, MTime: in.MTime}
	f, err := os.OpenFile(s.Engine.StagePath(u.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	if err := s.Engine.Store.CreateUpload(u); err != nil {
		os.Remove(f.Name())
		return err
	}
	return writeJSON(w, map[string]any{"id": u.ID, "received": 0})
}

func (s *Server) uploadStatus(w http.ResponseWriter, r *http.Request) error {
	u, err := s.Engine.Store.Upload(r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, map[string]any{"id": u.ID, "received": u.Received, "size": u.Size})
}

func (s *Server) folderUploads(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := s.Engine.Store.UploadsIn(id)
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(list))
	for i, u := range list {
		out[i] = map[string]any{"id": u.ID, "parentId": u.ParentID, "name": u.Name, "size": u.Size,
			"received": u.Received, "encrypted": u.Encrypted, "mtime": u.MTime}
	}
	return writeJSON(w, out)
}

func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	lock, _ := s.uploadLocks.LoadOrStore(id, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	if !mu.TryLock() {
		return httpError{http.StatusConflict, "Upload läuft bereits"}
	}
	defer mu.Unlock()

	u, err := s.Engine.Store.Upload(id)
	if err != nil {
		return err
	}
	offset, err := strconv.ParseInt(r.Header.Get("X-Upload-Offset"), 10, 64)
	if err != nil || offset != u.Received {
		w.Header().Set("X-Upload-Offset", strconv.FormatInt(u.Received, 10))
		return httpError{http.StatusConflict, "falscher Offset"}
	}
	f, err := os.OpenFile(s.Engine.StagePath(id), os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	limit := min(u.Size-offset, maxChunk)
	n, copyErr := io.Copy(f, io.LimitReader(r.Body, limit))
	// Whatever arrived counts, so an interrupted chunk is not sent twice.
	if err := f.Sync(); err != nil {
		return err
	}
	if err := s.Engine.Store.SetUploadReceived(id, offset+n); err != nil {
		return err
	}
	if copyErr != nil {
		return copyErr
	}
	if offset+n < u.Size {
		return writeJSON(w, map[string]any{"received": offset + n})
	}
	f.Close()
	node, err := s.Engine.Commit(u.ParentID, u.Name, s.Engine.StagePath(id), u.Encrypted, u.MTime, u.Overwrite)
	if err != nil {
		return err
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindUpload, "„%s“ im Browser hochgeladen (%d Bytes)", u.Name, u.Size)
	s.Engine.Store.DeleteUpload(id)
	s.uploadLocks.Delete(id)
	return writeJSON(w, map[string]any{"received": u.Size, "node": node})
}

func (s *Server) cancelUpload(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := s.Engine.Store.Upload(id); err != nil {
		return err
	}
	if err := s.Engine.Store.DeleteUpload(id); err != nil {
		return err
	}
	os.Remove(s.Engine.StagePath(id))
	s.uploadLocks.Delete(id)
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) error {
	list, err := s.Engine.Store.Shares()
	if err != nil {
		return err
	}
	return writeJSON(w, list)
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		NodeID       int64
		Password     string
		ExpiresHours int
		MaxDownloads int
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.NodeID == store.RootID {
		return badRequest("der Stammordner kann nicht geteilt werden")
	}
	if in.ExpiresHours < 0 || in.MaxDownloads < 0 {
		return badRequest("ungültige Werte")
	}
	target, err := s.Engine.Store.Get(in.NodeID)
	if err != nil {
		return err
	}
	sh := &store.Share{Token: auth.Token(24), NodeID: in.NodeID, MaxDownloads: in.MaxDownloads}
	if in.ExpiresHours > 0 {
		sh.ExpiresAt = time.Now().Add(time.Duration(in.ExpiresHours) * time.Hour).Unix()
	}
	if in.Password != "" {
		hash, err := auth.HashPassword(in.Password)
		if err != nil {
			return err
		}
		sh.PasswordHash = hash
	}
	if err := s.Engine.Store.CreateShare(sh); err != nil {
		return err
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindShare, "Freigabe für „%s“ erstellt", target.Name)
	return writeJSON(w, map[string]string{"token": sh.Token})
}

func (s *Server) deleteShare(w http.ResponseWriter, r *http.Request) error {
	token := r.PathValue("token")
	name := "unbekannt"
	if list, err := s.Engine.Store.Shares(); err == nil {
		for _, sh := range list {
			if sh.Token == token {
				name = sh.Name
			}
		}
	}
	if err := s.Engine.Store.DeleteShare(token); err != nil {
		return err
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindShare, "Freigabe für „%s“ beendet", name)
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) error {
	u, err := s.Auth.User()
	if err != nil {
		return err
	}
	backup, _ := s.Engine.Store.Setting("backup_at")
	return writeJSON(w, map[string]any{"username": u.Username, "sftpEnabled": u.SFTP != "", "totpEnabled": u.TOTP != "", "backupAt": string(backup), "blockMiB": s.Engine.BlockMiB()})
}

// confirmPassword is required for every change to the credentials, so an
// unattended session cannot lock the owner out.
func (s *Server) confirmPassword(u *auth.User, password string) error {
	if !auth.CheckHash(u.Password, password) {
		return httpError{http.StatusForbidden, "aktuelles Kennwort ist falsch"}
	}
	return nil
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Current, Password string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u, err := s.Auth.User()
	if err != nil {
		return err
	}
	if err := s.confirmPassword(u, in.Current); err != nil {
		return err
	}
	if len(in.Password) < minPassword {
		return badRequest("Kennwort braucht mindestens 8 Zeichen")
	}
	if u.Password, err = auth.HashPassword(in.Password); err != nil {
		return err
	}
	if err := s.Auth.Save(u); err != nil {
		return err
	}
	s.Auth.LogoutAll()
	s.sessionCookie(w, r, s.Auth.NewSession(), int((7 * 24 * time.Hour).Seconds()))
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) setPassphrase(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Current, Passphrase string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u, err := s.Auth.User()
	if err != nil {
		return err
	}
	if err := s.confirmPassword(u, in.Current); err != nil {
		return err
	}
	if len(in.Passphrase) < minPassphrase {
		return badRequest("Passphrase braucht mindestens 10 Zeichen")
	}
	header, err := s.Engine.Vault().Header(in.Passphrase)
	if err != nil {
		return err
	}
	if err := s.Engine.Store.SetSetting("vault", header); err != nil {
		return err
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) setSFTP(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Current, Password string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u, err := s.Auth.User()
	if err != nil {
		return err
	}
	if err := s.confirmPassword(u, in.Current); err != nil {
		return err
	}
	if in.Password == "" {
		u.SFTP = ""
	} else {
		if len(in.Password) < minPassword {
			return badRequest("Kennwort braucht mindestens 8 Zeichen")
		}
		if u.SFTP, err = auth.HashPassword(in.Password); err != nil {
			return err
		}
	}
	if err := s.Auth.Save(u); err != nil {
		return err
	}
	if in.Password == "" {
		s.Engine.Store.Log(store.LevelInfo, store.KindSystem, "SFTP abgeschaltet")
	} else {
		s.Engine.Store.Log(store.LevelInfo, store.KindSystem, "SFTP-Kennwort gesetzt")
	}
	if s.OnSFTPChange != nil {
		if err := s.OnSFTPChange(); err != nil {
			return err
		}
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) setBlocks(w http.ResponseWriter, r *http.Request) error {
	var in struct{ MiB int }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.MiB < files.MinBlockMiB || in.MiB > files.MaxBlockMiB {
		return badRequest(fmt.Sprintf("Segmentgröße muss zwischen %d und %d MiB liegen", files.MinBlockMiB, files.MaxBlockMiB))
	}
	if err := s.Engine.SetBlockMiB(in.MiB); err != nil {
		return err
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindSystem, "Segmentgröße auf %d MiB gesetzt", in.MiB)
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) totpBegin(w http.ResponseWriter, r *http.Request) error {
	secret, uri, err := s.Auth.BeginTOTP()
	if err != nil {
		return err
	}
	return writeJSON(w, map[string]string{"secret": secret, "uri": uri})
}

func (s *Server) totpConfirm(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Code string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.Auth.ConfirmTOTP(in.Code); err != nil {
		return badRequest(err.Error())
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Current string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	u, err := s.Auth.User()
	if err != nil {
		return err
	}
	if err := s.confirmPassword(u, in.Current); err != nil {
		return err
	}
	u.TOTP = ""
	if err := s.Auth.Save(u); err != nil {
		return err
	}
	return writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) backupNow(w http.ResponseWriter, r *http.Request) error {
	if err := s.Engine.Backup(r.Context()); err != nil {
		if errors.Is(err, files.ErrNotReady) {
			return err
		}
		return fmt.Errorf("Backup: %w", err)
	}
	return writeJSON(w, map[string]bool{"ok": true})
}
