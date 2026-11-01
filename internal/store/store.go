// Package store persists all metadata in SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// RootID is the id of the root folder.
const RootID int64 = 1

// Node states.
const (
	StateReady   = "ready"
	StatePending = "pending"
	StateFailed  = "failed"
)

// Folder encryption policies.
const (
	PolicyInherit = 0
	PolicyEncrypt = 1
	PolicyPlain   = 2
)

var (
	ErrNotFound = errors.New("nicht gefunden")
	ErrExists   = errors.New("ein Eintrag mit diesem Namen existiert bereits")
	ErrCycle    = errors.New("ein Ordner kann nicht in sich selbst verschoben werden")
	ErrNotEmpty = errors.New("Ordner ist nicht leer")
)

const schema = `
CREATE TABLE IF NOT EXISTS nodes (
	id          INTEGER PRIMARY KEY,
	parent_id   INTEGER REFERENCES nodes(id) ON DELETE CASCADE,
	name        TEXT NOT NULL,
	is_dir      INTEGER NOT NULL DEFAULT 0,
	size        INTEGER NOT NULL DEFAULT 0,
	mime        TEXT NOT NULL DEFAULT '',
	encrypted   INTEGER NOT NULL DEFAULT 0,
	policy      INTEGER NOT NULL DEFAULT 0,
	state       TEXT NOT NULL DEFAULT 'ready',
	error       TEXT NOT NULL DEFAULT '',
	sha256      TEXT NOT NULL DEFAULT '',
	wrapped_key BLOB,
	stage_path  TEXT NOT NULL DEFAULT '',
	attempts    INTEGER NOT NULL DEFAULT 0,
	next_try    INTEGER NOT NULL DEFAULT 0,
	mtime       INTEGER NOT NULL,
	ctime       INTEGER NOT NULL,
	trashed_at  INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS nodes_unique_name ON nodes(parent_id, name) WHERE trashed_at IS NULL;
CREATE INDEX IF NOT EXISTS nodes_state ON nodes(state);
CREATE TABLE IF NOT EXISTS blocks (
	node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	idx     INTEGER NOT NULL,
	msg_id  INTEGER NOT NULL,
	size    INTEGER NOT NULL,
	PRIMARY KEY (node_id, idx)
);
CREATE TABLE IF NOT EXISTS orphans (msg_id INTEGER PRIMARY KEY);
CREATE TABLE IF NOT EXISTS shares (
	token         TEXT PRIMARY KEY,
	node_id       INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	password_hash TEXT NOT NULL DEFAULT '',
	expires_at    INTEGER NOT NULL DEFAULT 0,
	max_downloads INTEGER NOT NULL DEFAULT 0,
	downloads     INTEGER NOT NULL DEFAULT 0,
	created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS uploads (
	id         TEXT PRIMARY KEY,
	parent_id  INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	size       INTEGER NOT NULL,
	received   INTEGER NOT NULL DEFAULT 0,
	encrypted  INTEGER NOT NULL,
	overwrite  INTEGER NOT NULL DEFAULT 0,
	mtime      INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS events (
	id      INTEGER PRIMARY KEY,
	ts      INTEGER NOT NULL,
	level   TEXT NOT NULL,
	kind    TEXT NOT NULL,
	message TEXT NOT NULL
);
`

// Event levels.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Event kinds.
const (
	KindUpload   = "upload"
	KindDownload = "download"
	KindShare    = "share"
	KindDelete   = "delete"
	KindVerify   = "verify"
	KindLogin    = "login"
	KindTelegram = "telegram"
	KindSystem   = "system"
)

const eventsKept = 5000

// Event is one line of the protocol page.
type Event struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"`
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Log records an event. Logging must never break the operation it describes,
// so a failure here is only written to the process log.
func (s *Store) Log(level, kind, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if _, err := s.db.Exec(`INSERT INTO events(ts, level, kind, message) VALUES (?, ?, ?, ?)`, time.Now().Unix(), level, kind, msg); err != nil {
		log.Printf("event: %v", err)
		return
	}
	if level != LevelInfo {
		log.Printf("%s %s: %s", level, kind, msg)
	}
	s.db.Exec(`DELETE FROM events WHERE id <= (SELECT MAX(id) FROM events) - ?`, eventsKept)
}

// Events returns the newest events, filtered when level or kind are set.
func (s *Store) Events(level, kind string, limit int) ([]Event, error) {
	rows, err := s.db.Query(`SELECT id, ts, level, kind, message FROM events
		WHERE (? = '' OR level = ?) AND (? = '' OR kind = ?) ORDER BY id DESC LIMIT ?`, level, level, kind, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Kind, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClearEvents empties the protocol.
func (s *Store) ClearEvents() error {
	_, err := s.db.Exec(`DELETE FROM events`)
	return err
}

// migrations add columns to databases created by older versions.
var migrations = []struct{ table, column, def string }{
	{"nodes", "md5", "TEXT NOT NULL DEFAULT ''"},
	{"nodes", "verify_status", "TEXT NOT NULL DEFAULT ''"},
	{"nodes", "verify_note", "TEXT NOT NULL DEFAULT ''"},
	{"nodes", "verified_at", "INTEGER NOT NULL DEFAULT 0"},
}

// Verification results. VerifyError means the check could not finish, which
// says nothing about the file.
const (
	VerifyOK    = "ok"
	VerifyBad   = "bad"
	VerifyError = "error"
)

// Node is a file or a folder.
type Node struct {
	ID           int64  `json:"id"`
	ParentID     int64  `json:"parentId"`
	Name         string `json:"name"`
	IsDir        bool   `json:"isDir"`
	Size         int64  `json:"size"`
	Mime         string `json:"mime"`
	Encrypted    bool   `json:"encrypted"`
	Policy       int    `json:"policy"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	MD5          string `json:"md5,omitempty"`
	VerifyStatus string `json:"verifyStatus,omitempty"`
	VerifyNote   string `json:"verifyNote,omitempty"`
	VerifiedAt   int64  `json:"verifiedAt,omitempty"`
	MTime        int64  `json:"mtime"`
	CTime        int64  `json:"ctime"`
	TrashedAt    int64  `json:"trashedAt,omitempty"`
	Shared       bool   `json:"shared"`
	Segments     int64  `json:"segments"`
	// FileCount is the number of files below a folder.
	FileCount  int64  `json:"fileCount,omitempty"`
	WrappedKey []byte `json:"-"`
	StagePath  string `json:"-"`
	Attempts   int    `json:"-"`
}

// Stats summarizes the whole storage.
type Stats struct {
	Files      int64 `json:"files"`
	Folders    int64 `json:"folders"`
	Segments   int64 `json:"segments"`
	Size       int64 `json:"size"`
	Stored     int64 `json:"stored"`
	Encrypted  int64 `json:"encrypted"`
	Plain      int64 `json:"plain"`
	Shares     int64 `json:"shares"`
	TrashFiles int64 `json:"trashFiles"`
	TrashSize  int64 `json:"trashSize"`
	LastUpload int64 `json:"lastUpload"`
	Unverified int64 `json:"unverified"`
	Corrupted  int64 `json:"corrupted"`
}

// Block is one Telegram message that holds a part of a file.
type Block struct {
	Index int
	MsgID int
	Size  int64
}

// Share is a public link.
type Share struct {
	Token        string `json:"token"`
	NodeID       int64  `json:"nodeId"`
	Name         string `json:"name"`
	IsDir        bool   `json:"isDir"`
	PasswordHash string `json:"-"`
	HasPassword  bool   `json:"hasPassword"`
	Trashed      bool   `json:"trashed"`
	ExpiresAt    int64  `json:"expiresAt"`
	MaxDownloads int    `json:"maxDownloads"`
	Downloads    int    `json:"downloads"`
	CreatedAt    int64  `json:"createdAt"`
}

// Upload is an unfinished resumable web upload.
type Upload struct {
	ID        string
	ParentID  int64
	Name      string
	Size      int64
	Received  int64
	Encrypted bool
	Overwrite bool
	MTime     int64
	CreatedAt int64
}

// Store wraps the database.
type Store struct{ db *sql.DB }

// Open creates or opens the database.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection serializes all access. Queries always drain their rows
	// before anything else runs, so this cannot deadlock.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration: %w", err)
	}
	now := time.Now().Unix()
	if _, err := db.Exec(`INSERT OR IGNORE INTO nodes(id, parent_id, name, is_dir, policy, mtime, ctime) VALUES (?, NULL, '', 1, ?, ?, ?)`,
		RootID, PolicyEncrypt, now, now); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	for _, m := range migrations {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, m.table, m.column).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, m.table, m.column, m.def)); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Changes returns a counter that grows with every write since the start.
func (s *Store) Changes() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT total_changes()`).Scan(&n)
	return n, err
}

// Snapshot writes a consistent copy of the database to path.
func (s *Store) Snapshot(path string) error {
	_, err := s.db.Exec(`VACUUM INTO ?`, path)
	return err
}

const activeShare = `(s.expires_at = 0 OR s.expires_at > CAST(strftime('%s', 'now') AS INTEGER)) AND (s.max_downloads = 0 OR s.downloads < s.max_downloads)`

const nodeCols = `id, COALESCE(parent_id, 0), name, is_dir, size, mime, encrypted, policy, state, error, sha256, md5, verify_status, verify_note, verified_at,
	wrapped_key, stage_path, attempts, mtime, ctime, COALESCE(trashed_at, 0),
	EXISTS(SELECT 1 FROM shares s WHERE s.node_id = nodes.id AND ` + activeShare + `),
	(SELECT COUNT(*) FROM blocks b WHERE b.node_id = nodes.id)`

type scanner interface{ Scan(...any) error }

func scanNode(r scanner) (*Node, error) {
	var n Node
	err := r.Scan(&n.ID, &n.ParentID, &n.Name, &n.IsDir, &n.Size, &n.Mime, &n.Encrypted, &n.Policy, &n.State, &n.Error,
		&n.SHA256, &n.MD5, &n.VerifyStatus, &n.VerifyNote, &n.VerifiedAt,
		&n.WrappedKey, &n.StagePath, &n.Attempts, &n.MTime, &n.CTime, &n.TrashedAt, &n.Shared, &n.Segments)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &n, err
}

func (s *Store) nodes(query string, args ...any) ([]*Node, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Get returns a node, including trashed ones.
func (s *Store) Get(id int64) (*Node, error) {
	return scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id = ?`, id))
}

// Child looks a live node up by name.
func (s *Store) Child(parent int64, name string) (*Node, error) {
	return scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE parent_id = ? AND name = ? AND trashed_at IS NULL`, parent, name))
}

// List returns the live children of a folder.
func (s *Store) List(parent int64) ([]*Node, error) {
	list, err := s.nodes(`SELECT `+nodeCols+` FROM nodes WHERE parent_id = ? AND trashed_at IS NULL ORDER BY is_dir DESC, name COLLATE NOCASE`, parent)
	if err != nil {
		return nil, err
	}
	return list, s.addFolderTotals(list)
}

const liveTree = `WITH RECURSIVE t(id) AS (
	SELECT id FROM nodes WHERE parent_id = ? AND trashed_at IS NULL
	UNION ALL SELECT n.id FROM nodes n JOIN t ON n.parent_id = t.id WHERE n.trashed_at IS NULL) `

// addFolderTotals fills in what a folder contains, recursively.
func (s *Store) addFolderTotals(list []*Node) error {
	for _, n := range list {
		if !n.IsDir {
			continue
		}
		err := s.db.QueryRow(liveTree+`SELECT COALESCE(SUM(is_dir = 0), 0), COALESCE(SUM(size), 0),
			(SELECT COUNT(*) FROM blocks WHERE node_id IN (SELECT id FROM t))
			FROM nodes WHERE id IN (SELECT id FROM t)`, n.ID).Scan(&n.FileCount, &n.Size, &n.Segments)
		if err != nil {
			return err
		}
	}
	return nil
}

// Stats summarizes everything that is not in the trash, plus the trash itself.
func (s *Store) Stats() (*Stats, error) {
	st := &Stats{}
	err := s.db.QueryRow(liveTree+`SELECT
		COALESCE(SUM(is_dir = 0), 0), COALESCE(SUM(is_dir = 1), 0), COALESCE(SUM(size), 0),
		COALESCE(SUM(is_dir = 0 AND encrypted = 1), 0), COALESCE(SUM(is_dir = 0 AND encrypted = 0), 0),
		COALESCE(SUM(is_dir = 0 AND state = 'ready' AND verify_status = ''), 0),
		COALESCE(SUM(verify_status = 'bad'), 0),
		COALESCE(MAX(CASE WHEN is_dir = 0 AND state = 'ready' THEN ctime END), 0),
		(SELECT COUNT(*) FROM blocks WHERE node_id IN (SELECT id FROM t)),
		(SELECT COALESCE(SUM(size), 0) FROM blocks WHERE node_id IN (SELECT id FROM t))
		FROM nodes WHERE id IN (SELECT id FROM t)`, RootID).
		Scan(&st.Files, &st.Folders, &st.Size, &st.Encrypted, &st.Plain, &st.Unverified, &st.Corrupted, &st.LastUpload, &st.Segments, &st.Stored)
	if err != nil {
		return nil, err
	}
	var allFiles, allSize int64
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM nodes WHERE is_dir = 0`).Scan(&allFiles, &allSize); err != nil {
		return nil, err
	}
	st.TrashFiles, st.TrashSize = allFiles-st.Files, allSize-st.Size
	err = s.db.QueryRow(liveTree+`SELECT COUNT(*) FROM shares s WHERE s.node_id IN (SELECT id FROM t) AND `+activeShare, RootID).Scan(&st.Shares)
	return st, err
}

// SetVerify stores the result of a check. A check also fills in an MD5 that
// files from older versions do not have yet.
func (s *Store) SetVerify(id int64, status, note, md5 string) error {
	_, err := s.db.Exec(`UPDATE nodes SET verify_status = ?, verify_note = ?, verified_at = ?,
		md5 = CASE WHEN md5 = '' THEN ? ELSE md5 END WHERE id = ?`, status, note, time.Now().Unix(), md5, id)
	return err
}

// Search finds live nodes by name.
func (s *Store) Search(q string) ([]*Node, error) {
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	all, err := s.nodes(`SELECT `+nodeCols+` FROM nodes WHERE name LIKE ? ESCAPE '\' AND id != ? AND trashed_at IS NULL ORDER BY is_dir DESC, name COLLATE NOCASE LIMIT 500`, like, RootID)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, n := range all {
		if s.alive(n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// alive reports whether no ancestor is in the trash.
func (s *Store) alive(n *Node) bool {
	for n.ParentID != 0 {
		p, err := s.Get(n.ParentID)
		if err != nil || p.TrashedAt != 0 {
			return false
		}
		n = p
	}
	return true
}

// Path returns the chain of nodes from the root down to id.
func (s *Store) Path(id int64) ([]*Node, error) {
	var path []*Node
	for id != 0 {
		n, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		path = append([]*Node{n}, path...)
		id = n.ParentID
	}
	return path, nil
}

// EncryptByDefault resolves the effective policy of a folder.
func (s *Store) EncryptByDefault(folder int64) (bool, error) {
	for {
		n, err := s.Get(folder)
		if err != nil {
			return true, err
		}
		if n.Policy != PolicyInherit {
			return n.Policy == PolicyEncrypt, nil
		}
		if n.ParentID == 0 {
			return true, nil
		}
		folder = n.ParentID
	}
}

// SetPolicy sets a folder's encryption policy.
func (s *Store) SetPolicy(id int64, policy int) error {
	_, err := s.db.Exec(`UPDATE nodes SET policy = ? WHERE id = ? AND is_dir = 1`, policy, id)
	return err
}

func uniqueErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrExists
	}
	return err
}

// Mkdir creates a folder.
func (s *Store) Mkdir(parent int64, name string) (*Node, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO nodes(parent_id, name, is_dir, mtime, ctime) VALUES (?, ?, 1, ?, ?)`, parent, name, now, now)
	if err != nil {
		return nil, uniqueErr(err)
	}
	id, _ := res.LastInsertId()
	return s.Get(id)
}

// InsertFile stores a new file. An existing file of that name goes to the trash
// in the same transaction, so a name never points at nothing.
func (s *Store) InsertFile(n *Node, replace bool) (*Node, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if replace {
		if _, err := tx.Exec(`UPDATE nodes SET trashed_at = ? WHERE parent_id = ? AND name = ? AND is_dir = 0 AND trashed_at IS NULL`, now, n.ParentID, n.Name); err != nil {
			return nil, err
		}
	}
	res, err := tx.Exec(`INSERT INTO nodes(parent_id, name, size, mime, encrypted, state, sha256, md5, wrapped_key, stage_path, mtime, ctime)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ParentID, n.Name, n.Size, n.Mime, n.Encrypted, n.State, n.SHA256, n.MD5, n.WrappedKey, n.StagePath, n.MTime, now)
	if err != nil {
		return nil, uniqueErr(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Get(id)
}

// Move renames and/or moves a node.
func (s *Store) Move(id, newParent int64, newName string) error {
	if id == RootID {
		return errors.New("der Stammordner kann nicht verändert werden")
	}
	for p := newParent; p != 0; {
		if p == id {
			return ErrCycle
		}
		n, err := s.Get(p)
		if err != nil {
			return err
		}
		if !n.IsDir {
			return errors.New("Ziel ist kein Ordner")
		}
		p = n.ParentID
	}
	_, err := s.db.Exec(`UPDATE nodes SET parent_id = ?, name = ? WHERE id = ?`, newParent, newName, id)
	return uniqueErr(err)
}

// SetMTime updates the modification time.
func (s *Store) SetMTime(id, mtime int64) error {
	_, err := s.db.Exec(`UPDATE nodes SET mtime = ? WHERE id = ?`, mtime, id)
	return err
}

// Trash moves a node to the trash.
func (s *Store) Trash(id int64) error {
	if id == RootID {
		return errors.New("der Stammordner kann nicht gelöscht werden")
	}
	_, err := s.db.Exec(`UPDATE nodes SET trashed_at = ? WHERE id = ? AND trashed_at IS NULL`, time.Now().Unix(), id)
	return err
}

// ListTrash returns the top-level trash entries.
func (s *Store) ListTrash() ([]*Node, error) {
	return s.nodes(`SELECT ` + nodeCols + ` FROM nodes WHERE trashed_at IS NOT NULL ORDER BY trashed_at DESC`)
}

// Restore takes a node out of the trash. If its name was taken in the meantime
// the restored node gets a suffix instead of failing.
func (s *Store) Restore(id int64) error {
	n, err := s.Get(id)
	if err != nil {
		return err
	}
	parent := n.ParentID
	if p, err := s.Get(parent); err != nil || p.TrashedAt != 0 || !s.alive(p) {
		parent = RootID
	}
	name := n.Name
	for i := 1; ; i++ {
		_, err := s.db.Exec(`UPDATE nodes SET trashed_at = NULL, parent_id = ?, name = ? WHERE id = ?`, parent, name, id)
		if err = uniqueErr(err); !errors.Is(err, ErrExists) {
			return err
		}
		name = fmt.Sprintf("%s (wiederhergestellt %d)", n.Name, i)
	}
}

// Purge removes a node and everything below it for good. Its Telegram messages
// are queued as orphans in the same transaction, so none can be forgotten.
// It returns the stage files that are no longer needed.
func (s *Store) Purge(id int64) ([]string, error) {
	if id == RootID {
		return nil, errors.New("der Stammordner kann nicht gelöscht werden")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	const tree = `WITH RECURSIVE t(id) AS (SELECT ? UNION ALL SELECT n.id FROM nodes n JOIN t ON n.parent_id = t.id) `
	if _, err := tx.Exec(tree+`INSERT OR IGNORE INTO orphans(msg_id) SELECT msg_id FROM blocks WHERE node_id IN (SELECT id FROM t)`, id); err != nil {
		return nil, err
	}
	rows, err := tx.Query(tree+`SELECT stage_path FROM nodes WHERE id IN (SELECT id FROM t) AND stage_path != ''`, id)
	if err != nil {
		return nil, err
	}
	var stages []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		stages = append(stages, p)
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, id); err != nil {
		return nil, err
	}
	return stages, tx.Commit()
}

// PurgeBefore returns trash entries older than the cutoff.
func (s *Store) PurgeBefore(cutoff int64) ([]*Node, error) {
	return s.nodes(`SELECT `+nodeCols+` FROM nodes WHERE trashed_at IS NOT NULL AND trashed_at < ?`, cutoff)
}

// Descendants returns all live files below a folder with their relative paths.
func (s *Store) Descendants(folder int64, prefix string) (map[string]*Node, error) {
	out := map[string]*Node{}
	children, err := s.List(folder)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		rel := prefix + c.Name
		if !c.IsDir {
			out[rel] = c
			continue
		}
		sub, err := s.Descendants(c.ID, rel+"/")
		if err != nil {
			return nil, err
		}
		for k, v := range sub {
			out[k] = v
		}
	}
	return out, nil
}

// NextPending returns the next file that is due for upload.
func (s *Store) NextPending(now int64) (*Node, error) {
	return scanNode(s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE state = 'pending' AND next_try <= ? ORDER BY next_try, id LIMIT 1`, now))
}

// Jobs returns everything that has not reached Telegram yet.
func (s *Store) Jobs() ([]*Node, error) {
	return s.nodes(`SELECT ` + nodeCols + ` FROM nodes WHERE state != 'ready' AND is_dir = 0 ORDER BY id`)
}

// StagePaths returns the stage files still referenced.
func (s *Store) StagePaths() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT stage_path FROM nodes WHERE stage_path != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// Retry schedules another upload attempt.
func (s *Store) Retry(id int64, next int64, msg string) error {
	_, err := s.db.Exec(`UPDATE nodes SET attempts = attempts + 1, next_try = ?, error = ? WHERE id = ?`, next, msg, id)
	return err
}

// RetryNow resets a job so it is picked up immediately.
func (s *Store) RetryNow(id int64) error {
	_, err := s.db.Exec(`UPDATE nodes SET state = CASE WHEN stage_path != '' THEN 'pending' ELSE state END, attempts = 0, next_try = 0, error = '' WHERE id = ? AND state != 'ready'`, id)
	return err
}

// Fail marks an upload as finally failed.
func (s *Store) Fail(id int64, msg string) error {
	_, err := s.db.Exec(`UPDATE nodes SET state = 'failed', error = ? WHERE id = ?`, msg, id)
	return err
}

// MarkReady finishes an upload.
func (s *Store) MarkReady(id int64) error {
	_, err := s.db.Exec(`UPDATE nodes SET state = 'ready', error = '', stage_path = '', attempts = 0 WHERE id = ?`, id)
	return err
}

// AddBlock records an uploaded block.
func (s *Store) AddBlock(node int64, b Block) error {
	_, err := s.db.Exec(`INSERT INTO blocks(node_id, idx, msg_id, size) VALUES (?, ?, ?, ?)`, node, b.Index, b.MsgID, b.Size)
	return err
}

// Blocks returns the blocks of a file in order.
func (s *Store) Blocks(node int64) ([]Block, error) {
	rows, err := s.db.Query(`SELECT idx, msg_id, size FROM blocks WHERE node_id = ? ORDER BY idx`, node)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Block
	for rows.Next() {
		var b Block
		if err := rows.Scan(&b.Index, &b.MsgID, &b.Size); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddOrphan queues a Telegram message for deletion.
func (s *Store) AddOrphan(msgID int) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO orphans(msg_id) VALUES (?)`, msgID)
	return err
}

// Orphans returns messages waiting for deletion.
func (s *Store) Orphans(limit int) ([]int, error) {
	rows, err := s.db.Query(`SELECT msg_id FROM orphans LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RemoveOrphans forgets deleted messages.
func (s *Store) RemoveOrphans(ids []int) error {
	for _, id := range ids {
		if _, err := s.db.Exec(`DELETE FROM orphans WHERE msg_id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// Setting returns a raw setting, or nil when unset.
func (s *Store) Setting(key string) ([]byte, error) {
	var v []byte
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

// SetSetting stores a raw setting.
func (s *Store) SetSetting(key string, value []byte) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// DeleteSetting removes a setting.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

const shareCols = `s.token, s.node_id, n.name, n.is_dir, s.password_hash, s.expires_at, s.max_downloads, s.downloads, s.created_at`

func scanShare(r scanner) (*Share, error) {
	var sh Share
	err := r.Scan(&sh.Token, &sh.NodeID, &sh.Name, &sh.IsDir, &sh.PasswordHash, &sh.ExpiresAt, &sh.MaxDownloads, &sh.Downloads, &sh.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	sh.HasPassword = sh.PasswordHash != ""
	return &sh, err
}

// CreateShare stores a share link.
func (s *Store) CreateShare(sh *Share) error {
	_, err := s.db.Exec(`INSERT INTO shares(token, node_id, password_hash, expires_at, max_downloads, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		sh.Token, sh.NodeID, sh.PasswordHash, sh.ExpiresAt, sh.MaxDownloads, time.Now().Unix())
	return err
}

// Share returns a link by token. Links of trashed files count as gone.
func (s *Store) Share(token string) (*Share, error) {
	sh, err := scanShare(s.db.QueryRow(`SELECT `+shareCols+` FROM shares s JOIN nodes n ON n.id = s.node_id WHERE s.token = ?`, token))
	if err != nil {
		return nil, err
	}
	n, err := s.Get(sh.NodeID)
	if err != nil {
		return nil, err
	}
	if n.TrashedAt != 0 || !s.alive(n) {
		return nil, ErrNotFound
	}
	return sh, nil
}

// Shares lists all links.
func (s *Store) Shares() ([]*Share, error) {
	rows, err := s.db.Query(`SELECT ` + shareCols + ` FROM shares s JOIN nodes n ON n.id = s.node_id ORDER BY s.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Share{}
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, sh := range out {
		n, err := s.Get(sh.NodeID)
		sh.Trashed = err != nil || n.TrashedAt != 0 || !s.alive(n)
	}
	return out, nil
}

// DeleteShare removes a link.
func (s *Store) DeleteShare(token string) error {
	_, err := s.db.Exec(`DELETE FROM shares WHERE token = ?`, token)
	return err
}

// DeleteSharesOf ends all links of a node and returns how many there were.
func (s *Store) DeleteSharesOf(node int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM shares WHERE node_id = ?`, node)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountDownload atomically consumes one download. It reports false when the
// limit is already used up.
func (s *Store) CountDownload(token string) (bool, error) {
	res, err := s.db.Exec(`UPDATE shares SET downloads = downloads + 1 WHERE token = ? AND (max_downloads = 0 OR downloads < max_downloads)`, token)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CreateUpload registers a resumable upload.
func (s *Store) CreateUpload(u *Upload) error {
	_, err := s.db.Exec(`INSERT INTO uploads(id, parent_id, name, size, encrypted, overwrite, mtime, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.ParentID, u.Name, u.Size, u.Encrypted, u.Overwrite, u.MTime, time.Now().Unix())
	return err
}

// Upload returns an unfinished upload.
func (s *Store) Upload(id string) (*Upload, error) {
	var u Upload
	err := s.db.QueryRow(`SELECT id, parent_id, name, size, received, encrypted, overwrite, mtime, created_at FROM uploads WHERE id = ?`, id).
		Scan(&u.ID, &u.ParentID, &u.Name, &u.Size, &u.Received, &u.Encrypted, &u.Overwrite, &u.MTime, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &u, err
}

// SetUploadReceived stores the upload offset.
func (s *Store) SetUploadReceived(id string, received int64) error {
	_, err := s.db.Exec(`UPDATE uploads SET received = ? WHERE id = ?`, received, id)
	return err
}

// DeleteUpload forgets an upload.
func (s *Store) DeleteUpload(id string) error {
	_, err := s.db.Exec(`DELETE FROM uploads WHERE id = ?`, id)
	return err
}

// StaleUploads returns uploads created before the cutoff.
func (s *Store) StaleUploads(cutoff int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM uploads WHERE created_at < ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
