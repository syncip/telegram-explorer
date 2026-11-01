package web

import (
	"html/template"
	"net/http"
	"strconv"
	"time"

	"telegram-explorer/internal/auth"
	"telegram-explorer/internal/store"
)

const grantTTL = 12 * time.Hour

var sharePage = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Freigabe</title>
<style>
:root{color-scheme:light dark;--bg:#f3f3f3;--card:#fff;--text:#1b1b1b;--muted:#616161;--line:#d9d9d9;--accent:#0067c0}
@media (prefers-color-scheme:dark){:root{--bg:#202020;--card:#2b2b2b;--text:#f3f3f3;--muted:#a9a9a9;--line:#3d3d3d;--accent:#60cdff}}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font:15px "Segoe UI",system-ui,sans-serif;padding:16px;box-sizing:border-box}
main{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:28px;width:min(420px,100%);box-sizing:border-box}
h1{font-size:18px;font-weight:600;margin:0 0 6px;overflow-wrap:anywhere}p{color:var(--muted);margin:0 0 20px}
input{width:100%;box-sizing:border-box;padding:8px 10px;margin-bottom:12px;border:1px solid var(--line);border-radius:4px;background:transparent;color:inherit;font:inherit}
a.btn,button{display:block;width:100%;box-sizing:border-box;text-align:center;background:var(--accent);color:#fff;border:0;border-radius:4px;padding:9px;font:inherit;font-weight:600;cursor:pointer;text-decoration:none}
@media (prefers-color-scheme:dark){a.btn,button{color:#00243a}}
.err{color:#c42b1c}
</style></head><body><main>
{{if .Gone}}<h1>Nicht verfügbar</h1><p>Diese Freigabe existiert nicht oder ist abgelaufen.</p>
{{else if .Locked}}<h1>{{.Name}}</h1><p>Der Speicher ist gerade gesperrt. Bitte später erneut versuchen.</p>
{{else if .NeedPassword}}<h1>{{.Name}}</h1><p>Diese Freigabe ist mit einem Kennwort geschützt.</p>
{{if .Error}}<p class="err">{{.Error}}</p>{{end}}
<form method="post"><input type="password" name="password" aria-label="Kennwort" placeholder="Kennwort" autofocus required><button>Weiter</button></form>
{{else}}<h1>{{.Name}}</h1><p>{{if .IsDir}}Ordner, wird als ZIP geladen{{else}}{{.Size}}{{end}}</p><a class="btn" href="/s/{{.Token}}/dl">Herunterladen</a>{{end}}
</main></body></html>`))

type shareView struct {
	Gone, Locked, NeedPassword, IsDir bool
	Name, Size, Token, Error          string
}

func humanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

func (s *Server) activeShare(token string) (*store.Share, error) {
	sh, err := s.Engine.Store.Share(token)
	if err != nil {
		return nil, err
	}
	if sh.ExpiresAt != 0 && time.Now().Unix() > sh.ExpiresAt {
		return nil, store.ErrNotFound
	}
	if sh.MaxDownloads != 0 && sh.Downloads >= sh.MaxDownloads {
		return nil, store.ErrNotFound
	}
	return sh, nil
}

func (s *Server) renderShare(w http.ResponseWriter, code int, v shareView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(code)
	sharePage.Execute(w, v)
}

func grantCookie(token string) string { return "telex_share_" + token }

func (s *Server) granted(r *http.Request, sh *store.Share) bool {
	if sh.PasswordHash == "" {
		return true
	}
	c, err := r.Cookie(grantCookie(sh.Token))
	if err != nil {
		return false
	}
	exp, ok := s.grants.Load(sh.Token + ":" + c.Value)
	return ok && time.Now().Before(exp.(time.Time))
}

func (s *Server) sharePage(w http.ResponseWriter, r *http.Request) {
	sh, err := s.activeShare(r.PathValue("token"))
	if err != nil {
		s.renderShare(w, http.StatusNotFound, shareView{Gone: true})
		return
	}
	s.renderShareState(w, r, sh, "")
}

func (s *Server) renderShareState(w http.ResponseWriter, r *http.Request, sh *store.Share, msg string) {
	v := shareView{Name: sh.Name, IsDir: sh.IsDir, Token: sh.Token, Error: msg}
	if s.Engine.Vault() == nil {
		v.Locked = true
		s.renderShare(w, http.StatusServiceUnavailable, v)
		return
	}
	v.NeedPassword = !s.granted(r, sh)
	if n, err := s.Engine.Store.Get(sh.NodeID); err == nil {
		v.Size = humanSize(n.Size)
	}
	s.renderShare(w, http.StatusOK, v)
}

func (s *Server) shareUnlock(w http.ResponseWriter, r *http.Request) {
	sh, err := s.activeShare(r.PathValue("token"))
	if err != nil {
		s.renderShare(w, http.StatusNotFound, shareView{Gone: true})
		return
	}
	key := "share:" + sh.Token + ":" + s.clientIP(r)
	if s.Auth.Limiter.Blocked(key) > 0 {
		s.renderShareState(w, r, sh, "Zu viele Fehlversuche, bitte später erneut versuchen.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if sh.PasswordHash == "" || !auth.CheckHash(sh.PasswordHash, r.FormValue("password")) {
		s.Auth.Limiter.Fail(key)
		s.Engine.Store.Log(store.LevelWarn, store.KindShare, "Falsches Kennwort für Freigabe „%s“ von %s", sh.Name, s.clientIP(r))
		s.renderShareState(w, r, sh, "Kennwort ist falsch.")
		return
	}
	s.Auth.Limiter.Success(key)
	grant := auth.Token(24)
	s.grants.Store(sh.Token+":"+grant, time.Now().Add(grantTTL))
	http.SetCookie(w, &http.Cookie{Name: grantCookie(sh.Token), Value: grant, Path: "/s/" + sh.Token, HttpOnly: true,
		Secure: s.secure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(grantTTL.Seconds())})
	http.Redirect(w, r, "/s/"+sh.Token, http.StatusSeeOther)
}

func (s *Server) shareDownload(w http.ResponseWriter, r *http.Request) {
	s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		sh, err := s.activeShare(r.PathValue("token"))
		if err != nil {
			return err
		}
		if !s.granted(r, sh) {
			http.Redirect(w, r, "/s/"+sh.Token, http.StatusSeeOther)
			return nil
		}
		if s.Engine.Vault() == nil {
			return httpError{http.StatusServiceUnavailable, "Speicher ist gesperrt"}
		}
		n, err := s.Engine.Store.Get(sh.NodeID)
		if err != nil {
			return err
		}
		// A range could read a file piece by piece without ever using up the
		// limit, so limited links always serve the whole file.
		if sh.MaxDownloads != 0 {
			r.Header.Del("Range")
		}
		if r.Method == http.MethodGet && r.Header.Get("Range") == "" {
			ok, err := s.Engine.Store.CountDownload(sh.Token)
			if err != nil {
				return err
			}
			if !ok {
				return store.ErrNotFound
			}
		}
		s.logDownload(r, "über Freigabe geladen von "+s.clientIP(r), n.Name)
		if n.IsDir {
			entries, err := s.Engine.Store.Descendants(n.ID, n.Name+"/")
			if err != nil {
				return err
			}
			return s.writeZip(w, r, n.Name+".zip", entries)
		}
		return s.serveFile(w, r, n, false)
	})(w, r)
}
