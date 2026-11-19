package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unicode/utf16"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/store"
)

// Live editing: everyone who has a text file open edits the same document on
// the server. The protocol is the one of CodeMirror's collab package. Clients
// push the changes they made on top of the version they know; a push based on
// an older version is refused, the client pulls what it missed, rebases its
// own changes and pushes again. Pulls wait until something happens.
//
// The server keeps the text, writes a draft to disk shortly after every change
// and stores a new version of the file once nobody typed for a while, when the
// last editor leaves or when the server shuts down. Drafts left over from a
// crash are stored as soon as the vault opens again.

var (
	liveDraftDelay  = 2 * time.Second
	liveSaveIdle    = 30 * time.Second
	liveSaveMax     = 5 * time.Minute
	livePeerTimeout = 45 * time.Second
	livePollWait    = 25 * time.Second
	liveTick        = time.Second
)

const liveMaxUpdates = 100 // per push

type liveUpdate struct {
	ClientID string          `json:"clientID"`
	Changes  json.RawMessage `json:"changes"`
}

type livePeer struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Anchor int       `json:"anchor"`
	Head   int       `json:"head"`
	seen   time.Time // last pull
}

type liveDoc struct {
	epoch   string
	node    *store.Node // the current version; its id changes with every save
	text    []uint16
	bom     bool
	crlf    bool
	updates []liveUpdate // all since the session started; version = len(updates)
	changes []changeSet  // parsed updates, for mapping cursors
	peers   map[string]*livePeer
	rev     int           // counts changes of peers and save state
	wake    chan struct{} // closed and replaced whenever something changed
	saved   int           // version last stored as a file
	drafted int           // version last written as a draft
	first   time.Time     // first change not stored as a file yet
	last    time.Time     // latest change
	savedAt int64
	problem string // why the file cannot be stored, shown to the editors
	gone    bool
}

type liveHub struct {
	mu   sync.Mutex
	docs map[int64]*liveDoc // by every version id the session went through
	once sync.Once
	stop chan struct{}
}

func (d *liveDoc) version() int { return len(d.updates) }

func (d *liveDoc) notify() {
	close(d.wake)
	d.wake = make(chan struct{})
}

func (s *Server) liveInit() {
	s.live.once.Do(func() {
		s.live.docs = map[int64]*liveDoc{}
		s.live.stop = make(chan struct{})
		go s.liveLoop()
	})
}

func (s *Server) draftDir() string { return filepath.Join(filepath.Dir(s.Engine.Dir), "drafts") }

func (s *Server) draftPath(d *liveDoc) string { return filepath.Join(s.draftDir(), d.epoch+".json") }

// liveDocOf finds the session of a request; the caller holds the lock.
func (s *Server) liveDocOf(r *http.Request, epoch string) (*liveDoc, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return nil, err
	}
	d := s.live.docs[id]
	if d == nil || d.epoch != epoch {
		return nil, httpError{http.StatusGone, "Die Sitzung ist abgelaufen."}
	}
	return d, nil
}

// liveJoin opens a file for live editing, starting the session if needed.
func (s *Server) liveJoin(w http.ResponseWriter, r *http.Request) error {
	s.liveInit()
	var in struct{ ClientID, Name string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if in.ClientID == "" || len(in.ClientID) > 64 {
		return badRequest("clientID fehlt")
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}

	s.live.mu.Lock()
	d := s.live.docs[id]
	s.live.mu.Unlock()
	if d == nil {
		// Reading may download from Telegram, so it happens without the lock.
		n, err := s.Engine.Store.Get(id)
		if err != nil {
			return err
		}
		if n.IsDir {
			return badRequest("ist ein Ordner")
		}
		if !s.Engine.Store.Alive(n) {
			// An older version, for example after a restart stored a draft:
			// the editor continues with the file that has the name now.
			cur, err := s.Engine.Store.Child(n.ParentID, n.Name)
			if err != nil || cur.IsDir || !s.Engine.Store.Alive(cur) {
				return httpError{http.StatusConflict, "Die Datei wurde gelöscht."}
			}
			n, id = cur, cur.ID
		}
		if n.Size > maxEdit {
			return httpError{http.StatusRequestEntityTooLarge, "Die Datei ist zu groß für den Editor (höchstens 10 MB)."}
		}
		reader, err := s.Engine.Open(r.Context(), n)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(reader, maxEdit+1))
		reader.Close()
		if err != nil {
			return err
		}
		text, bom, crlf, err := textOf(data)
		if err != nil {
			return err
		}
		fresh := &liveDoc{epoch: auth.Token(12), node: n, text: text, bom: bom, crlf: crlf,
			peers: map[string]*livePeer{}, wake: make(chan struct{}), savedAt: n.MTime}
		s.live.mu.Lock()
		if d = s.live.docs[id]; d == nil {
			d = fresh
			s.live.docs[id] = d
		}
		s.live.mu.Unlock()
	}

	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.live.docs[id] != d {
		return httpError{http.StatusConflict, "Bitte noch einmal öffnen."}
	}
	name := in.Name
	if len(name) > 40 {
		name = name[:40]
	}
	d.peers[in.ClientID] = &livePeer{ID: in.ClientID, Name: name, seen: time.Now()}
	d.rev++
	d.notify()
	return writeJSON(w, map[string]any{
		"epoch":   d.epoch,
		"version": d.version(),
		"doc":     string(utf16.Decode(d.text)),
		"node":    d.node,
		"state":   d.state(in.ClientID),
	})
}

// state is what a pull reports besides the changes.
func (d *liveDoc) state(self string) map[string]any {
	peers := []*livePeer{}
	for _, p := range d.peers {
		if p.ID != self {
			peers = append(peers, p)
		}
	}
	return map[string]any{
		"rev":     d.rev,
		"peers":   peers,
		"pending": d.saved != d.version(),
		"savedAt": d.savedAt,
		"problem": d.problem,
		"gone":    d.gone,
		"node":    d.node,
	}
}

func (s *Server) livePush(w http.ResponseWriter, r *http.Request) error {
	s.liveInit()
	var in struct {
		Epoch    string
		ClientID string
		Version  int
		Updates  []json.RawMessage
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEdit*2)).Decode(&in); err != nil {
		return badRequest("ungültige Anfrage")
	}
	if len(in.Updates) > liveMaxUpdates {
		return badRequest("zu viele Änderungen auf einmal")
	}
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	d, err := s.liveDocOf(r, in.Epoch)
	if err != nil {
		return err
	}
	if d.gone {
		return httpError{http.StatusGone, d.problem}
	}
	if in.Version != d.version() {
		// The client missed changes; it pulls them and tries again.
		return writeJSON(w, map[string]any{"ok": false, "version": d.version()})
	}
	text := d.text
	parsed := make([]changeSet, len(in.Updates))
	for i, raw := range in.Updates {
		cs, err := parseChanges(raw)
		if err == nil {
			text, err = cs.apply(text)
		}
		if err != nil {
			return badRequest(err.Error())
		}
		parsed[i] = cs
	}
	if len(text) > maxEdit {
		return httpError{http.StatusRequestEntityTooLarge, "Die Datei ist zu groß für den Editor (höchstens 10 MB)."}
	}
	d.text = text
	for i, raw := range in.Updates {
		d.updates = append(d.updates, liveUpdate{ClientID: in.ClientID, Changes: raw})
		d.changes = append(d.changes, parsed[i])
		for _, p := range d.peers {
			p.Anchor, p.Head = parsed[i].mapPos(p.Anchor), parsed[i].mapPos(p.Head)
		}
	}
	now := time.Now()
	if d.first.IsZero() {
		d.first = now
	}
	d.last = now
	d.notify()
	return writeJSON(w, map[string]any{"ok": true, "version": d.version()})
}

// livePull waits until there are changes after version or the state differs
// from rev, then returns them.
func (s *Server) livePull(w http.ResponseWriter, r *http.Request) error {
	s.liveInit()
	s.streams.init()
	q := r.URL.Query()
	version, err1 := strconv.Atoi(q.Get("version"))
	rev, err2 := strconv.Atoi(q.Get("rev"))
	if err1 != nil || err2 != nil {
		return badRequest("ungültige Anfrage")
	}
	self := q.Get("clientID")
	timeout := time.NewTimer(livePollWait)
	defer timeout.Stop()
	for {
		s.live.mu.Lock()
		d, err := s.liveDocOf(r, q.Get("epoch"))
		if err != nil {
			s.live.mu.Unlock()
			return err
		}
		if version < 0 || version > d.version() {
			s.live.mu.Unlock()
			return badRequest("unbekannte Version")
		}
		p := d.peers[self]
		if p == nil {
			// Back after a timeout: the client shows up as an editor again.
			p = &livePeer{ID: self, Name: q.Get("name")}
			d.peers[self] = p
			d.rev++
		}
		p.seen = time.Now()
		if version < d.version() || rev != d.rev {
			out := map[string]any{"updates": d.updates[version:], "version": d.version(), "state": d.state(self)}
			s.live.mu.Unlock()
			return writeJSON(w, out)
		}
		wake := d.wake
		s.live.mu.Unlock()
		select {
		case <-wake:
		case <-timeout.C:
			return writeJSON(w, map[string]any{"updates": []liveUpdate{}, "version": version})
		case <-r.Context().Done():
			return nil
		case <-s.streams.done:
			return writeJSON(w, map[string]any{"updates": []liveUpdate{}, "version": version})
		}
	}
}

// livePresence stores where a client's cursor is, mapped to the latest version.
func (s *Server) livePresence(w http.ResponseWriter, r *http.Request) error {
	s.liveInit()
	var in struct {
		Epoch, ClientID, Name string
		Version, Anchor, Head int
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	d, err := s.liveDocOf(r, in.Epoch)
	if err != nil {
		return err
	}
	if in.Version < 0 || in.Version > d.version() || in.Anchor < 0 || in.Head < 0 {
		return badRequest("unbekannte Version")
	}
	for _, cs := range d.changes[in.Version:] {
		in.Anchor, in.Head = cs.mapPos(in.Anchor), cs.mapPos(in.Head)
	}
	size := len(d.text)
	p := d.peers[in.ClientID]
	if p == nil {
		p = &livePeer{ID: in.ClientID}
		d.peers[in.ClientID] = p
	}
	p.Name = in.Name
	if len(p.Name) > 40 {
		p.Name = p.Name[:40]
	}
	p.Anchor, p.Head, p.seen = min(in.Anchor, size), min(in.Head, size), time.Now()
	d.rev++
	d.notify()
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// liveLeave ends a client's part. The last one out stores the file.
func (s *Server) liveLeave(w http.ResponseWriter, r *http.Request) error {
	s.liveInit()
	var in struct{ Epoch, ClientID string }
	if err := readJSON(r, &in); err != nil {
		return err
	}
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	d, err := s.liveDocOf(r, in.Epoch)
	if err != nil {
		return err
	}
	delete(d.peers, in.ClientID)
	d.rev++
	d.notify()
	if len(d.peers) == 0 {
		s.liveSave(d)
		s.liveDrop(d)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// liveLoop writes drafts, stores files and ends sessions nobody uses.
func (s *Server) liveLoop() {
	t := time.NewTicker(liveTick)
	defer t.Stop()
	for {
		select {
		case <-s.live.stop:
			return
		case <-t.C:
		}
		s.live.mu.Lock()
		now := time.Now()
		for _, d := range s.liveDocs() {
			for id, p := range d.peers {
				if now.Sub(p.seen) > livePeerTimeout {
					delete(d.peers, id)
					d.rev++
					d.notify()
				}
			}
			dirty := d.saved != d.version()
			switch {
			case dirty && (len(d.peers) == 0 || now.Sub(d.last) >= liveSaveIdle || now.Sub(d.first) >= liveSaveMax):
				s.liveSave(d)
			case dirty && d.drafted != d.version() && now.Sub(d.last) >= liveDraftDelay:
				s.liveDraft(d)
			}
			if len(d.peers) == 0 && (d.saved == d.version() || d.gone) {
				s.liveDrop(d)
			}
		}
		s.live.mu.Unlock()
	}
}

// liveDocs lists each session once; the caller holds the lock.
func (s *Server) liveDocs() []*liveDoc {
	seen := map[*liveDoc]bool{}
	var out []*liveDoc
	for _, d := range s.live.docs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func (s *Server) liveDrop(d *liveDoc) {
	for id, x := range s.live.docs {
		if x == d {
			delete(s.live.docs, id)
		}
	}
	if d.saved == d.version() {
		os.Remove(s.draftPath(d))
	}
	d.notify()
}

// draft is what a crash leaves behind for liveRecover.
type draft struct {
	Node    int64  `json:"node"`
	Parent  int64  `json:"parent"`
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

func (s *Server) liveDraft(d *liveDoc) {
	if err := os.MkdirAll(s.draftDir(), 0o700); err != nil {
		return
	}
	data, _ := json.Marshal(draft{Node: d.node.ID, Parent: d.node.ParentID, Name: d.node.Name, Content: fileOf(d.text, d.bom, d.crlf)})
	tmp := s.draftPath(d) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil && os.Rename(tmp, s.draftPath(d)) == nil {
		d.drafted = d.version()
	}
}

// liveSave stores the text as a new version of the file; the caller holds the lock.
func (s *Server) liveSave(d *liveDoc) {
	if d.gone || d.saved == d.version() {
		return
	}
	n, err := s.Engine.Store.Get(d.node.ID)
	if err == nil && n.TrashedAt != 0 {
		// Deleted while open, unless another file took its place.
		if cur, cerr := s.Engine.Store.Child(n.ParentID, n.Name); cerr != nil || cur.IsDir {
			err = errors.New("deleted")
		}
	}
	if err == nil {
		if parent, perr := s.Engine.Store.Get(n.ParentID); perr != nil || !s.Engine.Store.Alive(parent) {
			err = errors.New("deleted")
		}
	}
	if err != nil {
		// Nothing to store it in: the draft keeps the text, the editors are told.
		s.liveDraft(d)
		d.gone = true
		d.problem = "Die Datei wurde gelöscht. Deine Änderungen sind nicht gespeichert, kopiere sie bei Bedarf."
		d.rev++
		d.notify()
		return
	}
	node, err := s.storeText(n, fileOf(d.text, d.bom, d.crlf))
	if err != nil {
		s.liveDraft(d)
		problem := "Speichern fehlgeschlagen: " + err.Error()
		if d.problem != problem {
			d.problem = problem
			d.rev++
			d.notify()
		}
		return
	}
	s.Engine.Store.Log(store.LevelInfo, store.KindUpload, "„%s“ im Live-Editor gespeichert (%d Bytes)", node.Name, node.Size)
	s.live.docs[node.ID] = d
	d.node = node
	d.saved = d.version()
	d.first = time.Time{}
	d.savedAt = time.Now().Unix()
	d.problem = ""
	d.rev++
	d.notify()
	os.Remove(s.draftPath(d))
}

// FlushLive stores every open document, for a shutdown.
func (s *Server) FlushLive() {
	s.liveInit()
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if s.Engine.Vault() == nil {
		return
	}
	for _, d := range s.liveDocs() {
		s.liveSave(d)
	}
}

// liveRecover stores drafts a crash left behind. It runs once the vault is open.
func (s *Server) liveRecover() {
	paths, _ := filepath.Glob(filepath.Join(s.draftDir(), "*.json"))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		var dr draft
		if err != nil || json.Unmarshal(data, &dr) != nil {
			continue
		}
		n, err := s.Engine.Store.Get(dr.Node)
		if err != nil || n.IsDir {
			// The file is gone for good; the draft goes next to where it was.
			n = &store.Node{ParentID: dr.Parent, Name: dr.Name}
			if parent, err := s.Engine.Store.Get(n.ParentID); err != nil || !s.Engine.Store.Alive(parent) {
				n.ParentID = store.RootID
			}
			n.Encrypted, _ = s.Engine.Store.EncryptByDefault(n.ParentID)
		} else if parent, err := s.Engine.Store.Get(n.ParentID); err != nil || !s.Engine.Store.Alive(parent) {
			n.ParentID = store.RootID
		}
		if _, err := s.storeText(n, dr.Content); err != nil {
			s.Engine.Store.Log(store.LevelError, store.KindSystem, "Entwurf von „%s“ nicht wiederhergestellt: %v", dr.Name, err)
			continue
		}
		s.Engine.Store.Log(store.LevelWarn, store.KindUpload, "„%s“ aus einem Entwurf des Live-Editors wiederhergestellt", n.Name)
		os.Remove(p)
	}
}
