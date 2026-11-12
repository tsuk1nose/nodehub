package hub

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"

	"nodehub/internal/remote"
	"nodehub/internal/wire"
)

func (s *Store) remoteSchema() error {
	_, e := s.db.Exec(`
 CREATE TABLE IF NOT EXISTS agent_identities(serial TEXT PRIMARY KEY REFERENCES nodes(serial),public_key BLOB NOT NULL,revoked INTEGER NOT NULL DEFAULT 0,last_seen TEXT NOT NULL DEFAULT '',info TEXT NOT NULL DEFAULT '{}');
 CREATE TABLE IF NOT EXISTS terminal_audit(id TEXT PRIMARY KEY,serial TEXT NOT NULL,actor TEXT NOT NULL,source TEXT NOT NULL,created_at TEXT NOT NULL,started_at TEXT NOT NULL DEFAULT '',ended_at TEXT NOT NULL DEFAULT '',reason TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS security_audit(id INTEGER PRIMARY KEY AUTOINCREMENT,serial TEXT NOT NULL,action TEXT NOT NULL,actor TEXT NOT NULL,source TEXT NOT NULL,created_at TEXT NOT NULL);
 INSERT OR IGNORE INTO schema_version VALUES(2);
 INSERT OR IGNORE INTO schema_version VALUES(3);
 CREATE TABLE IF NOT EXISTS terminal_ssh(serial TEXT PRIMARY KEY REFERENCES nodes(serial),username TEXT NOT NULL,private_key BLOB NOT NULL,host_key TEXT NOT NULL,ready INTEGER NOT NULL DEFAULT 0);
 INSERT OR IGNORE INTO schema_version VALUES(5);`)
	return e
}
func (s *Store) securityEvent(serial, action, actor, source string) error {
	_, e := s.db.Exec(`INSERT INTO security_audit(serial,action,actor,source,created_at) VALUES(?,?,?,?,?)`, serial, action, actor, source, now())
	return e
}
func (s *Store) agentEvent(serial, action, source string) error {
	return s.securityEvent(serial, action, serial, source)
}
func (s *Store) identity(serial string) (remote.Identity, error) {
	var i remote.Identity
	e := s.db.QueryRow(`SELECT public_key,revoked FROM agent_identities WHERE serial=?`, serial).Scan(&i.PublicKey, &i.Revoked)
	return i, e
}
func (s *Store) enroll(serial string, key []byte) error {
	_, e := s.db.Exec(`INSERT INTO agent_identities(serial,public_key) VALUES(?,?) ON CONFLICT(serial) DO NOTHING`, serial, key)
	if e != nil {
		return e
	}
	i, e := s.identity(serial)
	if e != nil || i.Revoked || string(i.PublicKey) != string(key) {
		return ErrConflict
	}
	return nil
}
func (s *Store) seen(serial string, info wire.Info) error {
	b, e := json.Marshal(info)
	if e != nil {
		return e
	}
	res, e := s.db.Exec(`UPDATE agent_identities SET last_seen=?,info=? WHERE serial=? AND revoked=0`, now(), string(b), serial)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrUnauthorized
	}
	return nil
}
func (s *Store) audit(v remote.Record) error {
	_, e := s.db.Exec(`INSERT INTO terminal_audit(id,serial,actor,source,created_at,started_at,ended_at,reason) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET started_at=CASE WHEN excluded.started_at='' THEN terminal_audit.started_at ELSE excluded.started_at END,ended_at=CASE WHEN terminal_audit.ended_at<>'' THEN terminal_audit.ended_at ELSE excluded.ended_at END,reason=CASE WHEN terminal_audit.ended_at<>'' THEN terminal_audit.reason ELSE excluded.reason END`, v.ID, v.Serial, v.Actor, v.Source, v.Created, v.Started, v.Ended, v.Reason)
	return e
}
func (a *Server) terminalOwner(r *http.Request) string {
	if !a.cfg.Auth.Enabled {
		return "external-admin"
	}
	if c, e := r.Cookie("nodehub_session"); e == nil {
		return digest(c.Value)
	}
	return ""
}
func (a *Server) ownerValid(owner string) bool {
	if !a.cfg.Auth.Enabled {
		return owner == "external-admin"
	}
	var expiry string
	e := a.s.db.QueryRow(`SELECT expires_at FROM sessions WHERE hash=?`, owner).Scan(&expiry)
	return e == nil && expiry > now()
}
func (a *Server) terminalRoutes() {
	a.sshProvisionRoutes()
	a.mux.HandleFunc("GET /api/agent-events", a.admin(func(w http.ResponseWriter, r *http.Request) {
		rows, e := a.s.db.Query(`SELECT serial,action,actor,source,created_at FROM security_audit ORDER BY id DESC LIMIT 200`)
		if e != nil {
			fail(w, 500, "Audit query failed")
			return
		}
		defer rows.Close()
		out := []map[string]string{}
		for rows.Next() {
			var id, action, actor, source, created string
			if e = rows.Scan(&id, &action, &actor, &source, &created); e != nil {
				fail(w, 500, "Audit query failed")
				return
			}
			out = append(out, map[string]string{"serial": id, "action": action, "actor": actor, "source": source, "created_at": created})
		}
		if rows.Err() != nil {
			fail(w, 500, "Audit query failed")
			return
		}
		reply(w, 200, out)
	}))

	a.mux.HandleFunc("GET /agent/control", a.gateway.Control)
	a.mux.HandleFunc("GET /agent/data", a.gateway.Data)
	a.mux.HandleFunc("POST /api/node/{serial}/agent-enroll", a.node(func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.Terminal.Enabled {
			fail(w, 503, "Native terminal is disabled")
			return
		}
		var v struct {
			PublicKey string `json:"public_key"`
		}
		if !decode(w, r, &v) {
			return
		}
		key, e := base64.StdEncoding.DecodeString(v.PublicKey)
		if e != nil || len(key) != ed25519.PublicKeySize {
			fail(w, 400, "Invalid Ed25519 public key")
			return
		}
		if e = a.s.enroll(r.PathValue("serial"), key); e != nil {
			fail(w, 409, "Agent identity is already bound or revoked; replacement is not allowed")
			return
		}
		if e = a.s.agentEvent(r.PathValue("serial"), "agent_enrolled", r.RemoteAddr); e != nil {
			fail(w, 500, "Cannot save the enrollment audit; retry the request")
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	}))
	a.mux.HandleFunc("GET /api/agents", a.admin(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]map[string]any{}
		rows, e := a.s.db.Query(`SELECT serial,revoked,last_seen,info FROM agent_identities`)
		if e != nil {
			fail(w, 500, "Query failed")
			return
		}
		for rows.Next() {
			var id, last, info string
			var revoked bool
			if e = rows.Scan(&id, &revoked, &last, &info); e != nil {
				rows.Close()
				fail(w, 500, "Query failed")
				return
			}
			var v wire.Info
			_ = json.Unmarshal([]byte(info), &v)
			configured := false
			out[id] = map[string]any{"online": false, "revoked": revoked, "last_seen": last, "info": v, "ssh_configured": configured}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			fail(w, 500, "Query failed")
			return
		}
		for id, row := range out {
			_, targetErr := a.sshTarget(id)
			row["ssh_configured"] = targetErr == nil
		}
		for id, v := range a.gateway.Statuses() {
			if row, ok := out[id]; ok && !row["revoked"].(bool) {
				row["online"] = v.Online
				row["last_seen"] = v.LastSeen
				row["info"] = v.Info
			}
		}
		reply(w, 200, map[string]any{"enabled": a.cfg.Terminal.Enabled, "nodes": out})
	}))
	a.mux.HandleFunc("POST /api/nodes/{serial}/agent-revoke", a.admin(func(w http.ResponseWriter, r *http.Request) {
		res, e := a.s.db.Exec(`UPDATE agent_identities SET revoked=1 WHERE serial=?`, r.PathValue("serial"))
		if e != nil {
			fail(w, 500, "Revocation failed")
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "Agent is not enrolled")
			return
		}
		a.gateway.Disconnect(r.PathValue("serial"))
		if e = a.s.agentEvent(r.PathValue("serial"), "agent_revoked", r.RemoteAddr); e != nil {
			fail(w, 500, "Agent revoked, but the audit could not be saved; check the database")
			return
		}
		reply(w, 200, map[string]any{"ok": true, "scope": "nodehub_agent_only"})
	}))
	a.mux.HandleFunc("POST /api/nodes/{serial}/terminal", a.admin(func(w http.ResponseWriter, r *http.Request) {
		if !a.gateway.Origin(r) {
			fail(w, 403, "Untrusted terminal origin")
			return
		}
		actor := a.cfg.Auth.Username
		if !a.cfg.Auth.Enabled {
			actor = "external-admin"
		}
		source, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			source = r.RemoteAddr
		}
		id, e := a.gateway.Create(r.PathValue("serial"), a.terminalOwner(r), actor, source)
		if e != nil {
			fail(w, 409, e.Error())
			return
		}
		reply(w, 201, map[string]string{"session_id": id})
	}))
	a.mux.HandleFunc("GET /api/terminal/{id}/ws", a.admin(func(w http.ResponseWriter, r *http.Request) {
		a.gateway.Browser(w, r, r.PathValue("id"), a.terminalOwner(r))
	}))
	a.mux.HandleFunc("GET /api/terminal-audit", a.admin(func(w http.ResponseWriter, r *http.Request) {
		rows, e := a.s.db.Query(`SELECT id,serial,actor,source,created_at,started_at,ended_at,reason FROM terminal_audit ORDER BY created_at DESC LIMIT 200`)
		if e != nil {
			fail(w, 500, "Audit query failed")
			return
		}
		defer rows.Close()
		out := []remote.Record{}
		for rows.Next() {
			var v remote.Record
			if e = rows.Scan(&v.ID, &v.Serial, &v.Actor, &v.Source, &v.Created, &v.Started, &v.Ended, &v.Reason); e != nil {
				fail(w, 500, "Audit query failed")
				return
			}
			out = append(out, v)
		}
		if rows.Err() != nil {
			fail(w, 500, "Audit query failed")
			return
		}
		reply(w, 200, out)
	}))
}
