package hub

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type HeadNode struct {
	ID         string `json:"id"`
	NodeKey    string `json:"nodeKey"`
	PreAuthKey struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	} `json:"preAuthKey"`
	Name     string   `json:"name"`
	Online   bool     `json:"online"`
	LastSeen string   `json:"lastSeen"`
	IPs      []string `json:"ipAddresses"`
	User     struct {
		ID string `json:"id"`
	} `json:"user"`
}
type MeshNode struct {
	ID     string `json:"_id"`
	Name   string `json:"name"`
	Host   string `json:"rname"`
	MeshID string `json:"meshid"`
	Conn   int    `json:"conn"`
}
type Upstream struct {
	cfg       Config
	store     *Store
	http      *http.Client
	mu        sync.Mutex
	provision sync.Mutex
}

func NewUpstream(c Config, s *Store) *Upstream {
	return &Upstream{cfg: c, store: s, http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(r *http.Request, v []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (u *Upstream) head(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if e := json.NewEncoder(&buf).Encode(body); e != nil {
			return e
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(u.cfg.Headscale.InternalURL, "/")+path, &buf)
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+u.cfg.Headscale.APIKey)
	r.Header.Set("Content-Type", "application/json")
	resp, e := u.http.Do(r)
	if e != nil {
		return errors.New("Headscale connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("Headscale HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}
func (u *Upstream) HeadNodes(ctx context.Context) ([]HeadNode, error) {
	var result struct {
		Nodes []HeadNode `json:"nodes"`
	}
	e := u.head(ctx, "GET", "/api/v1/node", nil, &result)
	return result.Nodes, e
}
func (u *Upstream) Preauth(ctx context.Context, id string) (string, error) {
	u.provision.Lock()
	defer u.provision.Unlock()
	if !u.cfg.Headscale.Enabled {
		return "", errors.New("Headscale not configured")
	}
	n, e := u.store.Node(id)
	if e != nil {
		return "", e
	}
	if n.HeadscaleID != "" {
		return "", errors.New("node is already bound to Headscale")
	}
	var key, expiry string
	e = u.store.db.QueryRow(`SELECT key,expires_at FROM preauth WHERE serial=?`, id).Scan(&key, &expiry)
	if e == nil && expiry > now() {
		return key, nil
	}
	if e != nil && e != sql.ErrNoRows {
		return "", e
	}
	expiry = time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339)
	var result struct {
		Key struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"preAuthKey"`
	}
	e = u.head(ctx, "POST", "/api/v1/preauthkey", map[string]any{"user": u.cfg.Headscale.UserID, "expiration": expiry, "reusable": false, "ephemeral": false, "aclTags": u.cfg.Headscale.Tags}, &result)
	if e != nil {
		return "", e
	}
	if result.Key.Key == "" {
		return "", errors.New("Headscale returned empty key")
	}
	_, e = u.store.db.Exec(`INSERT INTO preauth(serial,key,expires_at,preauth_id) VALUES(?,?,?,?) ON CONFLICT(serial) DO UPDATE SET key=excluded.key,expires_at=excluded.expires_at,preauth_id=excluded.preauth_id`, id, result.Key.Key, expiry, result.Key.ID)
	return result.Key.Key, e
}
func (u *Upstream) MeshNodes(ctx context.Context) ([]MeshNode, error) {
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	h := http.Header{}
	enc := base64.StdEncoding.EncodeToString
	h.Set("x-meshauth", enc([]byte(u.cfg.MeshCentral.Username))+","+enc([]byte(u.cfg.MeshCentral.Password)))
	c, resp, e := d.DialContext(ctx, u.cfg.MeshCentral.InternalURL, h)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if e != nil {
		return nil, errors.New("MeshCentral connection or authentication failed")
	}
	defer c.Close()
	c.SetReadLimit(8 << 20)
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if e = c.WriteJSON(map[string]any{"action": "nodes", "meshid": u.cfg.MeshCentral.MeshID, "responseid": "nodehub"}); e != nil {
		return nil, e
	}
	for {
		var m struct {
			Action     string                `json:"action"`
			ResponseID string                `json:"responseid"`
			Result     string                `json:"result"`
			Nodes      map[string][]MeshNode `json:"nodes"`
		}
		if e = c.ReadJSON(&m); e != nil {
			return nil, errors.New("MeshCentral did not return device list")
		}
		if m.Action == "close" {
			return nil, errors.New("MeshCentral authentication rejected or control connection closed")
		}
		if m.Action != "nodes" || m.ResponseID != "nodehub" {
			continue
		}
		if m.Result != "" && m.Result != "ok" {
			return nil, errors.New("MeshCentral denied device list")
		}
		out := []MeshNode{}
		for group, list := range m.Nodes {
			for _, n := range list {
				if n.MeshID == "" {
					n.MeshID = group
				}
				if n.MeshID == u.cfg.MeshCentral.MeshID {
					out = append(out, n)
				}
			}
		}
		return out, nil
	}
}
func (u *Upstream) BindHead(ctx context.Context, id, key string) error {
	if !u.cfg.Headscale.Enabled {
		return errors.New("Headscale not configured")
	}
	key = strings.TrimPrefix(key, "nodekey:")
	if len(key) != 64 {
		return invalidBinding()
	}
	ns, e := u.HeadNodes(ctx)
	if e != nil {
		return e
	}
	for _, n := range ns {
		if strings.TrimPrefix(n.NodeKey, "nodekey:") == key && strings.EqualFold(n.Name, id) && n.User.ID == u.cfg.Headscale.UserID {
			var previous string
			if e = u.store.db.QueryRow(`SELECT headscale_id FROM nodes WHERE serial=?`, id).Scan(&previous); e != nil {
				return e
			}
			if previous != "" && previous != n.ID {
				return invalidBinding()
			}
			if previous == "" {
				var issued, issuedID string
				if e = u.store.db.QueryRow(`SELECT key,preauth_id FROM preauth WHERE serial=?`, id).Scan(&issued, &issuedID); e != nil || issued == "" {
					return invalidBinding()
				}
				if issuedID != "" {
					if n.PreAuthKey.ID != issuedID {
						return invalidBinding()
					}
				} else if !matchesIssuedPreauth(issued, n.PreAuthKey.Key) {
					return invalidBinding()
				}

			}
			res, err := u.store.db.Exec(`UPDATE nodes SET headscale_id=?,headscale_key=? WHERE serial=? AND (headscale_id='' OR headscale_id=?)`, n.ID, key, id, n.ID)
			if err != nil {
				return invalidBinding()
			}
			count, _ := res.RowsAffected()
			if count != 1 {
				return invalidBinding()
			}
			_, e = u.store.db.Exec(`DELETE FROM preauth WHERE serial=?`, id)
			return e
		}
	}
	return invalidBinding()
}
func (u *Upstream) BindMesh(ctx context.Context, id, externalID string) error {
	if !u.cfg.MeshCentral.Enabled {
		return errors.New("MeshCentral not configured")
	}
	ns, e := u.MeshNodes(ctx)
	if e != nil {
		return e
	}
	for _, n := range ns {
		if n.ID == externalID && (strings.EqualFold(n.Name, id) || strings.EqualFold(n.Host, id)) && n.MeshID == u.cfg.MeshCentral.MeshID {
			var prev string
			if e = u.store.db.QueryRow(`SELECT mesh_id FROM nodes WHERE serial=?`, id).Scan(&prev); e != nil {
				return e
			}
			if prev != "" && prev != externalID {
				return invalidBinding()
			}
			res, err := u.store.db.Exec(`UPDATE nodes SET mesh_id=? WHERE serial=? AND (mesh_id='' OR mesh_id=?)`, externalID, id, externalID)
			if err != nil {
				return invalidBinding()
			}
			count, _ := res.RowsAffected()
			if count != 1 {
				return invalidBinding()
			}
			return nil
		}
	}
	return invalidBinding()
}
func (u *Upstream) Sync(ctx context.Context) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, src := range []string{"headscale", "meshcentral"} {
		enabled := u.cfg.Headscale.Enabled
		if src == "meshcentral" {
			enabled = u.cfg.MeshCentral.Enabled
		}
		if !enabled {
			_ = u.store.SyncStatus(src, "disabled", false)
			continue
		}
		ns, e := u.store.Nodes()
		if e != nil {
			continue
		}
		if src == "headscale" {
			list, e := u.HeadNodes(ctx)
			if e != nil {
				_ = u.store.SyncStatus(src, "error", false)
				continue
			}
			byID := map[string]HeadNode{}
			for _, n := range list {
				byID[n.ID] = n
			}
			for _, n := range ns {
				if n.HeadscaleID == "" {
					continue
				}
				state, seen, ips := "missing", n.HeadscaleSeen, n.Addresses
				if v, ok := byID[n.HeadscaleID]; ok {
					state = "offline"
					if v.Online {
						state = "online"
						seen = now()
					} else if v.LastSeen != "" {
						seen = v.LastSeen
					}
					ips = strings.Join(v.IPs, ", ")
				}
				_, _ = u.store.db.Exec(`UPDATE nodes SET headscale_state=?,headscale_seen=?,addresses=? WHERE serial=?`, state, seen, ips, n.Serial)
			}
		} else {
			list, e := u.MeshNodes(ctx)
			if e != nil {
				_ = u.store.SyncStatus(src, "error", false)
				continue
			}
			byID := map[string]MeshNode{}
			for _, n := range list {
				byID[n.ID] = n
			}
			for _, n := range ns {
				if n.MeshID == "" {
					continue
				}
				state, seen := "missing", n.MeshSeen
				if v, ok := byID[n.MeshID]; ok {
					state = "offline"
					if v.Conn&1 != 0 {
						state = "online"
						seen = now()
					}
				}
				_, _ = u.store.db.Exec(`UPDATE nodes SET mesh_state=?,mesh_seen=? WHERE serial=?`, state, seen, n.Serial)
			}
		}
		_ = u.store.SyncStatus(src, "ok", true)
	}
}
func (u *Upstream) Run(ctx context.Context) {
	u.Sync(ctx)
	t := time.NewTicker(u.cfg.PollInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			u.Sync(ctx)
		}
	}
}
func (u *Upstream) MeshURL(id string) string {
	t := u.cfg.MeshCentral.DeviceURLTemplate
	if t == "" || id == "" {
		return ""
	}
	v := strings.ReplaceAll(t, "{node_id}", url.QueryEscape(id))
	p, e := url.Parse(v)
	base, be := url.Parse(u.cfg.MeshCentral.PublicURL)
	if e != nil || be != nil || p.Scheme != "https" || p.Host != base.Host {
		return ""
	}
	return v
}

// Compatibility for pending registrations made before preauth_id was retained.
// Headscale 0.29.7 returns hskey-auth-<prefix>-***, not the original key.
func matchesIssuedPreauth(issued, returned string) bool {
	if issued == returned {
		return true
	}
	if !strings.HasPrefix(returned, "hskey-auth-") || !strings.HasSuffix(returned, "-***") {
		return false
	}
	prefix := strings.TrimSuffix(returned, "***")
	return len(prefix) >= 23 && strings.HasPrefix(issued, prefix)
}
