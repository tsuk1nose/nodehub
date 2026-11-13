package hub

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrUnauthorized = errors.New("invalid or unavailable credential")
var ErrConflict = errors.New("conflicting registration or upstream identity")

type Store struct{ db *sql.DB }
type Node struct {
	Serial         string `json:"serial"`
	CreatedAt      string `json:"created_at"`
	Stage          string `json:"stage"`
	Error          string `json:"error"`
	HeadscaleID    string `json:"headscale_id"`
	HeadscaleKey   string `json:"headscale_key"`
	HeadscaleState string `json:"headscale_state"`
	HeadscaleSeen  string `json:"headscale_seen"`
	Addresses      string `json:"addresses"`
	MeshID         string `json:"mesh_id"`
	MeshState      string `json:"mesh_state"`
	MeshSeen       string `json:"mesh_seen"`
	MeshURL        string `json:"mesh_url"`
}
type Token struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	ExpiresAt string `json:"expires_at"`
	MaxUses   int    `json:"max_uses"`
	Uses      int    `json:"uses"`
	Revoked   bool   `json:"revoked"`
	CreatedAt string `json:"created_at"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
func randomHex(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func serial() string {
	const chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 12)
	for i := range b {
		n, e := rand.Int(rand.Reader, big.NewInt(36))
		if e != nil {
			panic(e)
		}
		b[i] = chars[n.Int64()]
	}
	return string(b)
}
func OpenStore(path string) (*Store, error) {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", path)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY);
 INSERT OR IGNORE INTO schema_version VALUES(1);
 CREATE TABLE IF NOT EXISTS tokens(id TEXT PRIMARY KEY, hash TEXT UNIQUE NOT NULL,label TEXT NOT NULL,expires_at TEXT NOT NULL,max_uses INTEGER NOT NULL,uses INTEGER NOT NULL DEFAULT 0,revoked INTEGER NOT NULL DEFAULT 0,created_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nodes(serial TEXT PRIMARY KEY, request_hash TEXT UNIQUE NOT NULL,credential_hash TEXT NOT NULL,created_at TEXT NOT NULL,stage TEXT NOT NULL DEFAULT 'registered',error TEXT NOT NULL DEFAULT '',headscale_id TEXT NOT NULL DEFAULT '',headscale_key TEXT NOT NULL DEFAULT '',headscale_state TEXT NOT NULL DEFAULT 'unknown',headscale_seen TEXT NOT NULL DEFAULT '',addresses TEXT NOT NULL DEFAULT '',mesh_id TEXT NOT NULL DEFAULT '',mesh_state TEXT NOT NULL DEFAULT 'unknown',mesh_seen TEXT NOT NULL DEFAULT '');
 CREATE UNIQUE INDEX IF NOT EXISTS headscale_binding ON nodes(headscale_id) WHERE headscale_id!='';
 CREATE UNIQUE INDEX IF NOT EXISTS mesh_binding ON nodes(mesh_id) WHERE mesh_id!='';
 CREATE TABLE IF NOT EXISTS sessions(hash TEXT PRIMARY KEY,expires_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS preauth(serial TEXT PRIMARY KEY REFERENCES nodes(serial),key TEXT NOT NULL,expires_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS sync_status(source TEXT PRIMARY KEY,state TEXT NOT NULL,last_success TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');`)
	if e != nil {
		db.Close()
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.remoteSchema(); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.ensurePreauthID(); e != nil {
		db.Close()
		return nil, e
	}

	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) NewToken(label, expiry string, max int) (Token, string, error) {
	t := Token{ID: randomHex(8), Label: label, ExpiresAt: expiry, MaxUses: max, CreatedAt: now()}
	secret := randomHex(32)
	if max < 0 || len(label) > 120 {
		return t, "", errors.New("invalid token settings")
	}
	if expiry != "" {
		v, e := time.Parse(time.RFC3339, expiry)
		if e != nil || !v.After(time.Now()) {
			return t, "", errors.New("expiry must be a future RFC3339 timestamp")
		}
		t.ExpiresAt = v.UTC().Format(time.RFC3339)
	}
	_, e := s.db.Exec(`INSERT INTO tokens(id,hash,label,expires_at,max_uses,created_at) VALUES(?,?,?,?,?,?)`, t.ID, digest(secret), label, t.ExpiresAt, max, t.CreatedAt)
	return t, secret, e
}
func (s *Store) Tokens() ([]Token, error) {
	r, e := s.db.Query(`SELECT id,label,expires_at,max_uses,uses,revoked,created_at FROM tokens ORDER BY created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []Token{}
	for r.Next() {
		var t Token
		if e = r.Scan(&t.ID, &t.Label, &t.ExpiresAt, &t.MaxUses, &t.Uses, &t.Revoked, &t.CreatedAt); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, r.Err()
}
func (s *Store) Register(ctx context.Context, token, requestID, credential string) (string, error) {
	if len(requestID) != 64 || len(credential) != 64 {
		return "", errors.New("request_id and credential must be 32-byte hex strings")
	}
	for _, v := range []string{requestID, credential} {
		if _, e := hex.DecodeString(v); e != nil {
			return "", errors.New("invalid hex")
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	var id, hash string
	e = tx.QueryRow(`SELECT serial,credential_hash FROM nodes WHERE request_hash=?`, digest(requestID)).Scan(&id, &hash)
	if e == nil {
		if hash != digest(credential) {
			return "", ErrUnauthorized
		}
		return id, tx.Commit()
	}
	if e != sql.ErrNoRows {
		return "", e
	}
	var tid, expiry string
	var max, uses, revoked int
	e = tx.QueryRow(`SELECT id,expires_at,max_uses,uses,revoked FROM tokens WHERE hash=?`, digest(token)).Scan(&tid, &expiry, &max, &uses, &revoked)
	if e != nil || revoked != 0 || (expiry != "" && expiry <= now()) || (max > 0 && uses >= max) {
		return "", ErrUnauthorized
	}
	for i := 0; i < 10; i++ {
		id = serial()
		_, e = tx.Exec(`INSERT INTO nodes(serial,request_hash,credential_hash,created_at) VALUES(?,?,?,?)`, id, digest(requestID), digest(credential), now())
		if e == nil {
			break
		}
		if !strings.Contains(e.Error(), "nodes.serial") {
			return "", e
		}
	}
	if e != nil {
		return "", e
	}
	if _, e = tx.Exec(`UPDATE tokens SET uses=uses+1 WHERE id=?`, tid); e != nil {
		return "", e
	}
	return id, tx.Commit()
}
func (s *Store) AuthenticateNode(id, key string) bool {
	if len(key) != 64 {
		return false
	}
	var hash string
	e := s.db.QueryRow(`SELECT credential_hash FROM nodes WHERE serial=?`, id).Scan(&hash)
	return e == nil && digest(key) == hash
}

const nodeColumns = `serial,created_at,stage,error,headscale_id,headscale_key,headscale_state,headscale_seen,addresses,mesh_id,mesh_state,mesh_seen`

func scanNode(r interface{ Scan(...any) error }) (Node, error) {
	var n Node
	e := r.Scan(&n.Serial, &n.CreatedAt, &n.Stage, &n.Error, &n.HeadscaleID, &n.HeadscaleKey, &n.HeadscaleState, &n.HeadscaleSeen, &n.Addresses, &n.MeshID, &n.MeshState, &n.MeshSeen)
	return n, e
}
func (s *Store) Nodes() ([]Node, error) {
	r, e := s.db.Query(`SELECT ` + nodeColumns + ` FROM nodes ORDER BY created_at DESC`)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []Node{}
	for r.Next() {
		n, e := scanNode(r)
		if e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, r.Err()
}
func (s *Store) Node(id string) (Node, error) {
	return scanNode(s.db.QueryRow(`SELECT `+nodeColumns+` FROM nodes WHERE serial=?`, id))
}
func (s *Store) Stage(id, stage, msg string) error {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, e := s.db.Exec(`UPDATE nodes SET stage=?,error=? WHERE serial=?`, stage, msg, id)
	return e
}
func (s *Store) Backup(path string) error {
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		return errors.New("backup target must not exist")
	}
	_, e := s.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(path, "'", "''") + `'`)
	if e == nil {
		e = os.Chmod(path, 0600)
	}
	return e
}
func (s *Store) SyncStatus(source, state string, ok bool, detail ...string) error {
	last := ""
	if ok {
		last = now()
	}
	_, e := s.db.Exec(`INSERT INTO sync_status(source,state,last_success,error) VALUES(?,?,?,?) ON CONFLICT(source) DO UPDATE SET state=excluded.state,last_success=CASE WHEN excluded.last_success='' THEN sync_status.last_success ELSE excluded.last_success END,error=excluded.error`, source, state, last, func() string {
		if state == "error" {
			if len(detail) > 0 {
				return detail[0]
			}
			return "Connection or authentication failed; preserving the last known state"
		}
		return ""
	}())
	return e
}
func (s *Store) Statuses() ([]map[string]string, error) {
	r, e := s.db.Query(`SELECT source,state,last_success,error FROM sync_status ORDER BY source`)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	out := []map[string]string{}
	for r.Next() {
		var a, b, c, d string
		if e = r.Scan(&a, &b, &c, &d); e != nil {
			return nil, e
		}
		out = append(out, map[string]string{"source": a, "state": b, "last_success": c, "error": d})
	}
	return out, r.Err()
}
func invalidBinding() error {
	return fmt.Errorf("%w: verify external ID, node name and group", ErrConflict)
}

// Retain the immutable upstream key ID: Headscale masks key material on reads.
func (s *Store) ensurePreauthID() error {
	rows, e := s.db.Query(`PRAGMA table_info(preauth)`)
	if e != nil {
		return e
	}
	exists := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if e = rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); e != nil {
			rows.Close()
			return e
		}
		if name == "preauth_id" {
			exists = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if !exists {
		if _, e = s.db.Exec(`ALTER TABLE preauth ADD COLUMN preauth_id TEXT NOT NULL DEFAULT ''`); e != nil {
			return e
		}
	}
	_, e = s.db.Exec(`INSERT OR IGNORE INTO schema_version VALUES(4)`)
	return e
}
