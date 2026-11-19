package web

import (
	"errors"
	"io"
	"net/http"
	"os"

	"telegram-explorer/internal/files"
	"telegram-explorer/internal/store"
)

// maxEdit is the largest text the editor saves in one request.
const maxEdit = 10 << 20

// saveContent stores an edited file as a new version. The old version moves to
// the trash, like an overwrite over SFTP, and its share links move along.
//
// The request names the version it was edited from. If that version is no
// longer the live file (saved elsewhere, overwritten, deleted), the save is
// refused with 409 and the id of the current file, unless ?force=1 asks to
// replace whatever has the name now.
func (s *Server) saveContent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	n, err := s.Engine.Store.Get(id)
	if err != nil {
		return err
	}
	if n.IsDir {
		return badRequest("ist ein Ordner")
	}
	if r.ContentLength > maxEdit {
		return httpError{http.StatusRequestEntityTooLarge, "Die Datei ist zu groß für den Editor (höchstens 10 MB)."}
	}
	parent, err := s.Engine.Store.Get(n.ParentID)
	if err != nil || !s.Engine.Store.Alive(parent) {
		return httpError{http.StatusConflict, "Der Ordner der Datei wurde gelöscht."}
	}
	if !s.Engine.Store.Alive(n) && r.URL.Query().Get("force") != "1" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		out := map[string]any{"error": "Die Datei wurde inzwischen woanders geändert oder gelöscht."}
		if cur, err := s.Engine.Store.Child(n.ParentID, n.Name); err == nil && !cur.IsDir {
			out["current"] = cur.ID
		}
		return writeJSON(w, out)
	}
	if s.Engine.Vault() == nil {
		return files.ErrLocked
	}

	f, err := s.Engine.NewStageFile()
	if err != nil {
		return err
	}
	written, err := io.Copy(f, io.LimitReader(r.Body, maxEdit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && written > maxEdit {
		err = httpError{http.StatusRequestEntityTooLarge, "Die Datei ist zu groß für den Editor (höchstens 10 MB)."}
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}

	// The live file may differ from the edited one after a forced save.
	old := n
	if cur, err := s.Engine.Store.Child(n.ParentID, n.Name); err == nil && !cur.IsDir {
		old = cur
	}
	node, err := s.Engine.Commit(n.ParentID, n.Name, f.Name(), old.Encrypted, 0, true)
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	if old.TrashedAt == 0 {
		if err := s.Engine.Store.MoveShares(old.ID, node.ID); err != nil {
			return err
		}
		s.Thumbs.Remove(old.ID)
	}
	if node.StagePath != "" {
		s.Thumbs.FromFile(node, node.StagePath)
	}
	if fresh, err := s.Engine.Store.Get(node.ID); err == nil {
		node = fresh
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindUpload, "„%s“ im Editor gespeichert (%d Bytes)", n.Name, written)
	s.marked(node)
	return writeJSON(w, node)
}

// newTextFile creates an empty file in a folder for the editor to open.
func (s *Server) newTextFile(w http.ResponseWriter, r *http.Request) error {
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
	parent, err := s.Engine.Store.Get(id)
	if err != nil {
		return err
	}
	if !parent.IsDir {
		return badRequest("Ziel ist kein Ordner")
	}
	if _, err := s.Engine.Store.Child(id, in.Name); err == nil {
		return store.ErrExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	encrypted, err := s.Engine.Store.EncryptByDefault(id)
	if err != nil {
		return err
	}
	f, err := s.Engine.NewStageFile()
	if err != nil {
		return err
	}
	f.Close()
	node, err := s.Engine.Commit(id, in.Name, f.Name(), encrypted, 0, false)
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindUpload, "„%s“ als neues Textdokument angelegt", in.Name)
	s.marked(node)
	return writeJSON(w, node)
}
