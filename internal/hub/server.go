package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"nodehub/internal/remote"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Server struct {
	gateway       *remote.Gateway
	targets       map[string]remote.Target
	cfg           Config
	s             *Store
	u             *Upstream
	mux           *http.ServeMux
	loginMu       sync.Mutex
	loginAttempts int
	loginWindow   time.Time
}

func NewServer(c Config, s *Store) *Server {
	return NewServerWithTargets(c, s, map[string]remote.Target{})
}
func NewServerWithTargets(c Config, s *Store, targets map[string]remote.Target) *Server {
	a := &Server{cfg: c, s: s, u: NewUpstream(c, s), mux: http.NewServeMux()}
	_, _ = s.db.Exec(`UPDATE terminal_audit SET ended_at=strftime('%Y-%m-%dT%H:%M:%SZ','now'),reason='server_restarted' WHERE ended_at=''`)
	a.targets = targets
	a.gateway = remote.New(c.Terminal, targets, remote.Hooks{Event: s.agentEvent, Identity: s.identity, Seen: s.seen, Audit: s.audit, OwnerValid: a.ownerValid, Target: a.sshTarget})
	a.routes()
	a.terminalRoutes()
	return a
}
func (a *Server) RunSync(ctx context.Context) { go a.gateway.Run(ctx); a.u.Run(ctx) }
func (a *Server) Close()                      { a.gateway.Close() }
func (a *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-NodeHub-Request") != "1" {
			fail(w, 403, "Missing request validation header")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	}
	a.mux.ServeHTTP(w, r)
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, "Invalid request format")
		return false
	}
	if e := d.Decode(new(any)); e != io.EOF {
		fail(w, 400, "Exactly one JSON object is required")
		return false
	}
	return true
}
func (a *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.Auth.Enabled {
			c, e := r.Cookie("nodehub_session")
			if e != nil {
				fail(w, 401, "Sign in required")
				return
			}
			var expiry string
			e = a.s.db.QueryRow(`SELECT expires_at FROM sessions WHERE hash=?`, digest(c.Value)).Scan(&expiry)
			if e != nil || expiry <= now() {
				fail(w, 401, "Session expired")
				return
			}
		}
		next(w, r)
	}
}
func (a *Server) node(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !a.s.AuthenticateNode(r.PathValue("serial"), key) {
			fail(w, 401, "Invalid node credential")
			return
		}
		next(w, r)
	}
}
func (a *Server) routes() {
	a.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if e := a.s.db.PingContext(r.Context()); e != nil {
			fail(w, 503, "database unavailable")
			return
		}
		reply(w, 200, map[string]string{"status": "ok"})
	})
	a.mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.Auth.Enabled {
			reply(w, 200, map[string]any{"authenticated": true, "auth_enabled": false})
			return
		}
		a.admin(func(w http.ResponseWriter, r *http.Request) {
			reply(w, 200, map[string]any{"authenticated": true, "auth_enabled": true})
		})(w, r)
	})
	a.mux.HandleFunc("POST /api/login", a.login)
	a.mux.HandleFunc("POST /api/logout", a.admin(func(w http.ResponseWriter, r *http.Request) {
		if c, e := r.Cookie("nodehub_session"); e == nil {
			_, _ = a.s.db.Exec(`DELETE FROM sessions WHERE hash=?`, digest(c.Value))
			a.gateway.DisconnectOwner(digest(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: "nodehub_session", Value: "", Path: "/", HttpOnly: true, Secure: a.cfg.Auth.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		reply(w, 200, map[string]bool{"ok": true})
	}))
	a.mux.HandleFunc("GET /api/nodes", a.admin(func(w http.ResponseWriter, r *http.Request) {
		ns, e := a.s.Nodes()
		if e != nil {
			fail(w, 500, "Query failed")
			return
		}
		for i := range ns {
			ns[i].MeshURL = a.u.MeshURL(ns[i].MeshID)
		}
		reply(w, 200, ns)
	}))
	a.mux.HandleFunc("GET /api/status", a.admin(func(w http.ResponseWriter, r *http.Request) {
		ss, e := a.s.Statuses()
		if e != nil {
			fail(w, 500, "Query failed")
			return
		}
		reply(w, 200, map[string]any{"sources": ss, "poll_interval_seconds": a.cfg.PollSeconds, "meshcentral_url": a.cfg.MeshCentral.PublicURL})
	}))
	a.mux.HandleFunc("GET /api/tokens", a.admin(func(w http.ResponseWriter, r *http.Request) {
		ts, e := a.s.Tokens()
		if e != nil {
			fail(w, 500, "Query failed")
			return
		}
		reply(w, 200, ts)
	}))
	a.mux.HandleFunc("POST /api/tokens", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Label   string `json:"label"`
			Expires string `json:"expires_at"`
			Max     int    `json:"max_uses"`
		}
		if !decode(w, r, &v) {
			return
		}
		t, secret, e := a.s.NewToken(v.Label, v.Expires, v.Max)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		reply(w, 201, map[string]any{"token": t, "secret": secret})
	}))
	a.mux.HandleFunc("POST /api/tokens/{id}/revoke", a.admin(func(w http.ResponseWriter, r *http.Request) {
		res, e := a.s.db.Exec(`UPDATE tokens SET revoked=1 WHERE id=?`, r.PathValue("id"))
		if e != nil {
			fail(w, 500, "Update failed")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "Token not found")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	a.mux.HandleFunc("POST /api/register", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Token      string `json:"token"`
			RequestID  string `json:"request_id"`
			Credential string `json:"credential"`
		}
		if !decode(w, r, &v) {
			return
		}
		id, e := a.s.Register(r.Context(), v.Token, v.RequestID, v.Credential)
		if errors.Is(e, ErrUnauthorized) {
			fail(w, 401, "Initialization token is invalid, expired, or exhausted")
			return
		}
		if e != nil {
			fail(w, 400, "Registration failed; check the request format and retry")
			return
		}
		reply(w, 200, map[string]string{"serial": id})
	})
	a.mux.HandleFunc("GET /api/node/{serial}", a.node(func(w http.ResponseWriter, r *http.Request) {
		n, e := a.s.Node(r.PathValue("serial"))
		if e != nil {
			fail(w, 404, "Node not found")
			return
		}
		reply(w, 200, map[string]any{"node": n, "headscale_url": a.cfg.Headscale.PublicURL})
	}))
	a.mux.HandleFunc("POST /api/node/{serial}/headscale-key", a.node(func(w http.ResponseWriter, r *http.Request) {
		key, e := a.u.Preauth(r.Context(), r.PathValue("serial"))
		if e != nil {
			fail(w, 502, "Cannot obtain Headscale registration credentials; check upstream configuration and bindings")
			return
		}
		reply(w, 200, map[string]string{"key": key, "login_server": a.cfg.Headscale.PublicURL})
	}))
	a.mux.HandleFunc("POST /api/node/{serial}/headscale-bind", a.node(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Key string `json:"node_key"`
		}
		if !decode(w, r, &v) {
			return
		}
		if e := a.u.BindHead(r.Context(), r.PathValue("serial"), v.Key); e != nil {
			fail(w, 409, "Cannot verify the Headscale node identity; retry later")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	a.mux.HandleFunc("POST /api/node/{serial}/stage", a.node(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Stage string `json:"stage"`
			Error string `json:"error"`
		}
		if !decode(w, r, &v) {
			return
		}
		switch v.Stage {
		case "registered", "hostname_ready", "tailscale_ready", "mesh_ready", "awaiting_binding", "awaiting_overlay", "complete", "failed":
		default:
			fail(w, 400, "Unknown stage")
			return
		}
		n, e := a.s.Node(r.PathValue("serial"))
		if e != nil {
			fail(w, 404, "Node not found")
			return
		}
		if v.Stage == "complete" && (n.HeadscaleID == "" || n.MeshID == "") {
			fail(w, 409, "Both upstream identities must be bound")
			return
		}
		if e = a.s.Stage(n.Serial, v.Stage, v.Error); e != nil {
			fail(w, 500, "Update failed")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	a.mux.HandleFunc("GET /api/meshcentral/candidates", a.admin(func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.MeshCentral.Enabled {
			fail(w, 503, "MeshCentral is not configured")
			return
		}
		ns, e := a.u.MeshNodes(r.Context())
		if e != nil {
			fail(w, 502, "MeshCentral query failed")
			return
		}
		reply(w, 200, ns)
	}))
	a.mux.HandleFunc("POST /api/nodes/{serial}/meshcentral-bind", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			ID string `json:"node_id"`
		}
		if !decode(w, r, &v) {
			return
		}
		if e := a.u.BindMesh(r.Context(), r.PathValue("serial"), v.ID); e != nil {
			fail(w, 409, "External device ID, name, or group does not match, or the device is already bound")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	a.mux.HandleFunc("POST /api/nodes/{serial}/revoke", a.admin(func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotImplemented, "Linked revocation awaits MeshCentral 1.3.1 device validation; neither upstream is partially revoked")
	}))
	a.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Endpoint not found") })
	a.mux.HandleFunc("/", a.static)
}
func (a *Server) login(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Auth.Enabled {
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	a.loginMu.Lock()
	if time.Since(a.loginWindow) > time.Minute {
		a.loginWindow = time.Now()
		a.loginAttempts = 0
	}
	a.loginAttempts++
	limited := a.loginAttempts > 20
	a.loginMu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "Too many sign-in attempts; retry later")
		return
	}
	var v struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &v) {
		return
	}
	if len(v.Password) > 72 {
		fail(w, 401, "Incorrect username or password")
		return
	}
	valid := bcrypt.CompareHashAndPassword([]byte(a.cfg.Auth.PasswordHash), []byte(v.Password)) == nil
	if subtle.ConstantTimeCompare([]byte(v.Username), []byte(a.cfg.Auth.Username)) != 1 || !valid {
		fail(w, 401, "Incorrect username or password")
		return
	}
	secret := randomHex(32)
	expiry := time.Now().UTC().Add(time.Duration(a.cfg.Auth.SessionHours) * time.Hour)
	_, _ = a.s.db.Exec(`DELETE FROM sessions WHERE expires_at<=?`, now())
	_, e := a.s.db.Exec(`INSERT INTO sessions(hash,expires_at) VALUES(?,?)`, digest(secret), expiry.Format(time.RFC3339))
	if e != nil {
		fail(w, 500, "Cannot create session")
		return
	}
	if e = a.s.securityEvent("", "admin_login", a.cfg.Auth.Username, r.RemoteAddr); e != nil {
		_, _ = a.s.db.Exec(`DELETE FROM sessions WHERE hash=?`, digest(secret))
		fail(w, 500, "Cannot save the sign-in audit")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "nodehub_session", Value: secret, Path: "/", HttpOnly: true, Secure: a.cfg.Auth.SecureCookie, SameSite: http.SameSiteStrictMode, Expires: expiry})
	reply(w, 200, map[string]bool{"ok": true})
}
func (a *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	path := filepath.Join(a.cfg.WebDir, filepath.Clean("/"+r.URL.Path))
	if info, e := os.Stat(path); e == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	index := filepath.Join(a.cfg.WebDir, "index.html")
	if _, e := os.Stat(index); e != nil {
		reply(w, 200, map[string]string{"service": "nodehub", "message": "Build web assets or run the Vite development server"})
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, index)
}
