package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"nodehub/internal/wire"
)

type Identity struct {
	PublicKey []byte
	Revoked   bool
}
type Record struct {
	ID      string `json:"id"`
	Serial  string `json:"serial"`
	Actor   string `json:"actor"`
	Source  string `json:"source"`
	Created string `json:"created_at"`
	Started string `json:"started_at"`
	Ended   string `json:"ended_at"`
	Reason  string `json:"reason"`
}
type Hooks struct {
	Target     func(string) (Target, error)
	Event      func(string, string, string) error
	Identity   func(string) (Identity, error)
	Seen       func(string, wire.Info) error
	Audit      func(Record) error
	OwnerValid func(string) bool
}
type Gateway struct {
	cfg        Config
	targets    map[string]Target
	hooks      Hooks
	mu         sync.Mutex
	devices    map[string]*device
	sessions   map[string]*session
	handshakes chan struct{}
	stopped    bool
}
type device struct {
	reason  atomic.Value
	serial  string
	ws      *websocket.Conn
	writeMu sync.Mutex
	info    wire.Info
	seen    time.Time
	done    chan struct{}
	once    sync.Once
}

func (d *device) send(v wire.Message) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	_ = d.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return d.ws.WriteJSON(v)
}
func (d *device) close() { d.closeAs("agent_disconnected") }
func (d *device) closeAs(reason string) {
	d.once.Do(func() { d.reason.Store(reason); close(d.done); _ = d.ws.Close() })
}
func (d *device) closeReason() string {
	if r := d.reason.Load(); r != nil {
		return r.(string)
	}
	return "agent_disconnected"
}

type session struct {
	id, serial, owner, actor, source string
	dev                              *device
	created                          time.Time
	last                             atomic.Int64
	token                            [32]byte
	consumed                         bool
	claimed                          bool
	ready                            chan struct{}
	done                             chan struct{}
	once                             sync.Once
	mu                               sync.Mutex
	tunnel                           net.Conn
	browser                          *websocket.Conn
	sshCloser                        interface{ Close() error }
	target                           Target
	started                          string
	writeMu                          sync.Mutex
	outputMu                         sync.Mutex
}
type Status struct {
	Online     bool      `json:"online"`
	LastSeen   string    `json:"last_seen"`
	Info       wire.Info `json:"info"`
	Configured bool      `json:"ssh_configured"`
}

func New(c Config, targets map[string]Target, h Hooks) *Gateway {
	c.Defaults()
	return &Gateway{cfg: c, targets: targets, hooks: h, devices: map[string]*device{}, sessions: map[string]*session{}, handshakes: make(chan struct{}, 64)}
}
func random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (g *Gateway) Origin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	u, e := url.Parse(origin)
	if e != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return false
	}
	for _, v := range g.cfg.AllowedOrigins {
		if origin == strings.TrimRight(v, "/") {
			return true
		}
	}
	return false
}
func (g *Gateway) Statuses() map[string]Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]Status{}
	for id, d := range g.devices {
		_, err := g.target(id)
		configured := err == nil
		out[id] = Status{true, d.seen.UTC().Format(time.RFC3339), d.info, configured}
	}
	return out
}
func (g *Gateway) Control(w http.ResponseWriter, r *http.Request) {
	if !g.cfg.Enabled || r.Header.Get("Origin") != "" {
		http.Error(w, "agent unavailable", 403)
		return
	}
	var release sync.Once
	select {
	case g.handshakes <- struct{}{}:
		defer release.Do(func() { <-g.handshakes })
	default:
		http.Error(w, "busy", 429)
		return
	}
	c, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	defer c.Close()
	c.SetReadLimit(8192)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	challenge := random()
	if e = c.WriteJSON(wire.Message{Type: "challenge", Challenge: challenge}); e != nil {
		return
	}
	var auth wire.Message
	if e = c.ReadJSON(&auth); e != nil || auth.Type != "auth" {
		return
	}
	identity, e := g.hooks.Identity(auth.Serial)
	if e != nil || identity.Revoked || len(identity.PublicKey) != ed25519.PublicKeySize {
		return
	}
	sig, e := base64.StdEncoding.DecodeString(auth.Signature)
	if e != nil || !ed25519.Verify(identity.PublicKey, wire.AuthMessage(auth.Serial, challenge), sig) {
		return
	}
	release.Do(func() { <-g.handshakes })
	if g.hooks.Event != nil {
		if e = g.hooks.Event(auth.Serial, "agent_authenticated", r.RemoteAddr); e != nil {
			return
		}
	}
	d := &device{serial: auth.Serial, ws: c, done: make(chan struct{}), seen: time.Now()}
	// Enrollment/revocation is checked again while publishing this connection.
	g.mu.Lock()
	identity, e = g.hooks.Identity(auth.Serial)
	if g.stopped || e != nil || identity.Revoked {
		g.mu.Unlock()
		return
	}
	old := g.devices[auth.Serial]
	g.devices[auth.Serial] = d
	g.mu.Unlock()
	if old != nil {
		old.closeAs("agent_replaced")
		g.closeDeviceSessions(old, "agent_replaced")
	}
	defer func() {
		g.mu.Lock()
		if g.devices[d.serial] == d {
			delete(g.devices, d.serial)
		}
		g.mu.Unlock()
		d.close()
		g.closeDeviceSessions(d, d.closeReason())
		if g.hooks.Event != nil {
			if err := g.hooks.Event(d.serial, d.closeReason(), r.RemoteAddr); err != nil {
				slog.Error("agent event audit failed")
			}
		}
	}()
	if e = d.send(wire.Message{Type: "authenticated", Token: "", Info: nil}); e != nil {
		return
	}
	_ = c.SetReadDeadline(time.Now().Add(g.cfg.Timeout()))
	for {
		var m wire.Message
		if e = c.ReadJSON(&m); e != nil {
			return
		}
		if m.Type != "heartbeat" || m.Info == nil {
			return
		}
		info := *m.Info
		if len(info.OSVersion) > 128 || len(info.OS) > 64 || len(info.Arch) > 32 || len(info.Version) > 64 || info.UptimeSeconds < 0 {
			return
		}
		if e = g.hooks.Seen(d.serial, info); e != nil {
			return
		}
		g.mu.Lock()
		d.info = info
		d.seen = time.Now()
		g.mu.Unlock()
		_ = c.SetReadDeadline(time.Now().Add(g.cfg.Timeout()))
		if e = d.send(wire.Message{Type: "heartbeat_ack"}); e != nil {
			return
		}
	}
}
func (g *Gateway) Create(serial, owner, actor, source string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.cfg.Enabled || g.stopped {
		return "", errors.New("Native terminal is disabled")
	}
	if g.hooks.OwnerValid != nil && !g.hooks.OwnerValid(owner) {
		return "", errors.New("The management session has expired")
	}
	target, err := g.target(serial)
	if err != nil {
		return "", errors.New("This node has not completed automatic SSH provisioning")
	}
	d := g.devices[serial]
	if d == nil {
		return "", errors.New("Agent is offline")
	}
	count := 0
	for _, s := range g.sessions {
		if s.serial == serial {
			count++
		}
	}
	if len(g.sessions) >= g.cfg.MaxSessions || count >= g.cfg.MaxPerNode {
		return "", errors.New("The terminal concurrency limit has been reached")
	}
	s := &session{target: target, id: random(), serial: serial, owner: owner, actor: actor, source: source, dev: d, created: time.Now(), ready: make(chan struct{}), done: make(chan struct{})}
	s.last.Store(time.Now().UnixNano())
	if e := g.hooks.Audit(s.record("", "")); e != nil {
		return "", errors.New("Cannot save the audit record")
	}
	g.sessions[s.id] = s
	return s.id, nil
}
func (s *session) record(end, reason string) Record {
	return Record{s.id, s.serial, s.actor, s.source, s.created.UTC().Format(time.RFC3339), s.started, end, reason}
}
func (g *Gateway) closeSession(s *session, reason string) {
	s.once.Do(func() {
		close(s.done)
		g.mu.Lock()
		delete(g.sessions, s.id)
		g.mu.Unlock()
		s.mu.Lock()
		if s.browser != nil {
			_ = s.browser.Close()
		}
		if s.sshCloser != nil {
			_ = s.sshCloser.Close()
		}
		if s.tunnel != nil {
			_ = s.tunnel.Close()
		}
		record := s.record(time.Now().UTC().Format(time.RFC3339), reason)
		s.mu.Unlock()
		if e := g.hooks.Audit(record); e != nil {
			slog.Error("terminal audit finalization failed", "session", s.id)
		}
	})
}
func (g *Gateway) closeDeviceSessions(d *device, reason string) {
	g.mu.Lock()
	ss := []*session{}
	for _, s := range g.sessions {
		if s.dev == d {
			ss = append(ss, s)
		}
	}
	g.mu.Unlock()
	for _, s := range ss {
		g.closeSession(s, reason)
	}
}
func (g *Gateway) Disconnect(serial string) {
	g.mu.Lock()
	d := g.devices[serial]
	g.mu.Unlock()
	if d != nil {
		d.closeAs("agent_revoked")
		g.closeDeviceSessions(d, "agent_revoked")
	}
}
func (g *Gateway) DisconnectOwner(owner string) {
	g.mu.Lock()
	ss := []*session{}
	for _, s := range g.sessions {
		if s.owner == owner {
			ss = append(ss, s)
		}
	}
	g.mu.Unlock()
	for _, s := range ss {
		g.closeSession(s, "logout")
	}
}
func (g *Gateway) Close() {
	g.mu.Lock()
	g.stopped = true
	ds := []*device{}
	ss := []*session{}
	for _, d := range g.devices {
		ds = append(ds, d)
	}
	for _, s := range g.sessions {
		ss = append(ss, s)
	}
	g.mu.Unlock()
	for _, d := range ds {
		d.closeAs("server_shutdown")
	}
	for _, s := range ss {
		g.closeSession(s, "server_shutdown")
	}
}
func (g *Gateway) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	defer g.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.mu.Lock()
			ss := []*session{}
			for _, s := range g.sessions {
				ss = append(ss, s)
			}
			g.mu.Unlock()
			for _, s := range ss {
				reason := ""
				if time.Since(s.created) > time.Duration(g.cfg.MaxSeconds)*time.Second {
					reason = "duration_limit"
				} else if time.Since(time.Unix(0, s.last.Load())) > time.Duration(g.cfg.IdleSeconds)*time.Second {
					reason = "idle_timeout"
				}
				s.mu.Lock()
				claimed := s.claimed
				s.mu.Unlock()
				if !claimed && time.Since(s.created) > 30*time.Second {
					reason = "browser_timeout"
				}
				if g.hooks.OwnerValid != nil && !g.hooks.OwnerValid(s.owner) {
					reason = "authorization_expired"
				}
				if reason != "" {
					g.closeSession(s, reason)
				}
			}
		}
	}
}
func (g *Gateway) Data(w http.ResponseWriter, r *http.Request) {
	if !g.cfg.Enabled || r.Header.Get("Origin") != "" {
		http.Error(w, "unavailable", 403)
		return
	}
	select {
	case g.handshakes <- struct{}{}:
		defer func() { <-g.handshakes }()
	default:
		http.Error(w, "busy", 429)
		return
	}
	c, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
	if e != nil {
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = c.Close()
		}
	}()
	c.SetReadLimit(4096)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var m wire.Message
	if e = c.ReadJSON(&m); e != nil || m.Type != "data" || len(m.Token) != 64 {
		return
	}
	g.mu.Lock()
	s := g.sessions[m.Session]
	if s == nil || s.serial != m.Serial || g.devices[m.Serial] != s.dev || time.Since(s.created) > 30*time.Second {
		g.mu.Unlock()
		return
	}
	s.mu.Lock()
	g.mu.Unlock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return
	default:
	}
	if s.consumed || s.token != sha256.Sum256([]byte(m.Token)) {
		return
	}
	s.consumed = true
	s.token = [32]byte{}
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	s.tunnel = wire.NewConn(c)
	close(s.ready)
	keep = true
}

func (g *Gateway) target(serial string) (Target, error) {
	if g.hooks.Target != nil {
		return g.hooks.Target(serial)
	}
	if v, ok := g.targets[serial]; ok {
		return v, nil
	}
	return Target{}, errors.New("SSH target unavailable")
}
