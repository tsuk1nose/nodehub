package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
	"nodehub/internal/edge"
	"nodehub/internal/remote"
	"nodehub/internal/wire"
)

type terminalFixture struct {
	t      *testing.T
	s      *Store
	a      *Server
	server *httptest.Server
	id     string
	cookie *http.Cookie
	agent  edge.Config
	cancel context.CancelFunc
	resize chan [2]uint32
}

func signer(t *testing.T) ssh.Signer {
	t.Helper()
	_, k, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	s, e := ssh.NewSignerFromKey(k)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func fakeSSH(t *testing.T, host, authorized ssh.Signer, lookup ...func() (remote.Target, error)) (int, chan [2]uint32) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	resize := make(chan [2]uint32, 20)
	var mu sync.Mutex
	connections := []net.Conn{}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, c := range connections {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(m ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		user, public := "maintainer", authorized.PublicKey()
		if len(lookup) > 0 {
			target, err := lookup[0]()
			if err != nil {
				return nil, fmt.Errorf("target unavailable")
			}
			user, public = target.User, target.Signer.PublicKey()
		}
		if m.User() != user || string(key.Marshal()) != string(public.Marshal()) {
			return nil, fmt.Errorf("denied")
		}
		return nil, nil
	}}
	// Real sshd offers several host keys; negotiation must choose the pinned one.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, err := ssh.NewSignerFromKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddHostKey(rsaSigner)
	cfg.AddHostKey(host)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := listener.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			connections = append(connections, raw)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				cc, channels, requests, e := ssh.NewServerConn(raw, cfg)
				if e != nil {
					return
				}
				defer cc.Close()
				go ssh.DiscardRequests(requests)
				for ch := range channels {
					if ch.ChannelType() != "session" {
						_ = ch.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					c, requests, e := ch.Accept()
					if e != nil {
						return
					}
					go func() {
						defer c.Close()
						for r := range requests {
							switch r.Type {
							case "pty-req":
								_ = r.Reply(true, nil)
							case "window-change":
								var size struct{ Cols, Rows, Width, Height uint32 }
								if ssh.Unmarshal(r.Payload, &size) == nil {
									select {
									case resize <- [2]uint32{size.Cols, size.Rows}:
									default:
									}
								}
							case "shell":
								_ = r.Reply(true, nil)
								_, _ = io.WriteString(c, "ready\r\n")
								go func() {
									buf := make([]byte, 4096)
									for {
										n, e := c.Read(buf)
										if e != nil {
											return
										}
										if _, e = c.Write(buf[:n]); e != nil {
											return
										}
									}
								}()
							default:
								if r.WantReply {
									_ = r.Reply(false, nil)
								}
							}
						}
					}()
				}
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, resize
}
func newTerminalFixture(t *testing.T, badHost bool, maxDuration ...int) *terminalFixture {
	return newTerminalFixtureMode(t, badHost, false, maxDuration...)
}
func newTerminalFixtureMode(t *testing.T, badHost, automatic bool, maxDuration ...int) *terminalFixture {
	t.Helper()
	s := testStore(t)
	_, token, _ := s.NewToken("terminal-test", "", 0)
	credential := randomHex(32)
	id, e := s.Register(context.Background(), token, randomHex(32), credential)
	if e != nil {
		t.Fatal(e)
	}
	host, client := signer(t), signer(t)
	var app *Server
	var port int
	var resized chan [2]uint32
	if automatic {
		port, resized = fakeSSH(t, host, client, func() (remote.Target, error) { return app.sshTarget(id) })
	} else {
		port, resized = fakeSSH(t, host, client)
	}
	pin := host.PublicKey()
	if badHost {
		pin = signer(t).PublicKey()
	}
	c := Config{PollSeconds: 60}
	c.Auth.Enabled = true
	c.Auth.Username = "admin"
	hash, _ := bcrypt.GenerateFromPassword([]byte("terminal-test-password"), bcrypt.MinCost)
	c.Auth.PasswordHash = string(hash)
	c.Auth.SessionHours = 1
	c.Terminal.Enabled = true
	c.Terminal.Defaults()
	c.Terminal.AllowedOrigins = []string{"https://console.test"}
	c.Terminal.MaxPerNode = 2
	if len(maxDuration) > 0 {
		c.Terminal.MaxSeconds = maxDuration[0]
	}
	server := httptest.NewUnstartedServer(nil)
	c.Terminal.AllowedOrigins = append(c.Terminal.AllowedOrigins, "https://"+server.Listener.Addr().String())
	if os.Getenv("NODEHUB_BROWSER_FIXTURE") != "" {
		c.WebDir, _ = filepath.Abs("../../web/dist")
	}
	targets := map[string]remote.Target{id: {User: "maintainer", Signer: client, HostKey: pin}}
	if automatic {
		targets = map[string]remote.Target{}
	}
	a := NewServerWithTargets(c, s, targets)
	app = a
	server.Config.Handler = a
	server.StartTLS()
	ctx, cancel := context.WithCancel(context.Background())
	go a.gateway.Run(ctx)
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	_ = os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	ac := edge.Config{Server: server.URL, IdentityFile: filepath.Join(dir, "identity.json"), CAFile: ca, SSHPort: port, HeartbeatSeconds: 1, ReconnectMaxSeconds: 1, MaxSessions: 2}
	bootstrap := filepath.Join(dir, "bootstrap.json")
	raw, _ := json.Marshal(map[string]string{"server": server.URL, "serial": id, "credential": credential})
	_ = os.WriteFile(bootstrap, raw, 0600)
	if e = edge.Enroll(ctx, ac, bootstrap); e != nil {
		t.Fatal(e)
	}
	if automatic {
		for _, suffix := range []string{"/ssh-provision", "/ssh-provision/confirm"} {
			body, _ := json.Marshal(map[string]string{"username": "root", "host_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey())))})
			req, _ := http.NewRequest("POST", server.URL+"/api/node/"+id+suffix, strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer "+credential)
			req.Header.Set("X-NodeHub-Request", "1")
			response, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal("auto SSH provisioning", response.StatusCode)
			}
		}
	}
	agent, e := edge.New(ac)
	if e != nil {
		t.Fatal(e)
	}
	agentDone := make(chan struct{})
	go func() { defer close(agentDone); _ = agent.Run(ctx) }()
	f := &terminalFixture{t, s, a, server, id, nil, ac, cancel, resized}
	t.Cleanup(func() {
		cancel()
		a.Close()
		select {
		case <-agentDone:
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
		server.Close()
	})
	response := f.call("/login", map[string]string{"username": "admin", "password": "terminal-test-password"}, nil, "")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	f.cookie = response.Cookies()[0]
	response.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.gateway.Statuses()[id].Online {
			return f
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent never connected")
	return nil
}
func (f *terminalFixture) call(path string, body any, cookie *http.Cookie, origin string) *http.Response {
	f.t.Helper()
	b, _ := json.Marshal(body)
	r, _ := http.NewRequest("POST", f.server.URL+"/api"+path, strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-NodeHub-Request", "1")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	resp, e := f.server.Client().Do(r)
	if e != nil {
		f.t.Fatal(e)
	}
	return resp
}
func (f *terminalFixture) create() string {
	f.t.Helper()
	r := f.call("/nodes/"+f.id+"/terminal", map[string]any{}, f.cookie, "https://console.test")
	defer r.Body.Close()
	if r.StatusCode != 201 {
		b, _ := io.ReadAll(r.Body)
		f.t.Fatal(r.StatusCode, string(b))
	}
	var v map[string]string
	_ = json.NewDecoder(r.Body).Decode(&v)
	return v["session_id"]
}
func (f *terminalFixture) dial(id, origin string, cookie *http.Cookie) (*websocket.Conn, *http.Response, error) {
	tlsCfg := f.server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	d := websocket.Dialer{TLSClientConfig: tlsCfg}
	h := http.Header{"Origin": []string{origin}}
	if cookie != nil {
		h.Set("Cookie", cookie.String())
	}
	return d.Dial("wss"+strings.TrimPrefix(f.server.URL, "https")+"/api/terminal/"+id+"/ws", h)
}
func readUntil(t *testing.T, c *websocket.Conn, want string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var text string
	for {
		kind, b, e := c.ReadMessage()
		if e != nil {
			t.Fatalf("read terminal (%q): %v", text, e)
		}
		if kind == websocket.BinaryMessage {
			text += string(b)
			if e = c.WriteJSON(map[string]any{"type": "ack", "bytes": len(b)}); e != nil {
				t.Fatal(e)
			}
		} else {
			var status map[string]string
			_ = json.Unmarshal(b, &status)
			text += status["message"]
		}
		if strings.Contains(text, want) {
			return
		}
	}
}
func waitAudit(t *testing.T, s *Store, id, reason string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var ended, got string
		_ = s.db.QueryRow(`SELECT ended_at,reason FROM terminal_audit WHERE id=?`, id).Scan(&ended, &got)
		if ended != "" && (reason == "" || got == reason) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("audit was not finalized", reason)
}
func TestNativeTerminalRoundTrip(t *testing.T) {
	f := newTerminalFixture(t, false)
	id := f.create()
	c, _, e := f.dial(id, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	readUntil(t, c, "ready")
	_ = c.WriteJSON(map[string]any{"type": "input", "data": "hello terminal\r"})
	readUntil(t, c, "hello terminal")
	_ = c.WriteJSON(map[string]any{"type": "resize", "cols": 100, "rows": 40})
	select {
	case size := <-f.resize:
		if size != [2]uint32{100, 40} {
			t.Fatal(size)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY resize not delivered")
	}
	_ = c.Close()
	waitAudit(t, f.s, id, "")
}
func TestNativeTerminalHostKeyMismatch(t *testing.T) {
	f := newTerminalFixture(t, true)
	id := f.create()
	c, _, e := f.dial(id, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	readUntil(t, c, "host key verification failed")
	waitAudit(t, f.s, id, "ssh_handshake_failed")
}
func TestNativeTerminalOriginOwnershipAndLimits(t *testing.T) {
	f := newTerminalFixture(t, false)
	r := f.call("/nodes/"+f.id+"/terminal", map[string]any{}, nil, "https://console.test")
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("unauthorized creation", r.StatusCode)
	}
	r = f.call("/nodes/"+f.id+"/terminal", map[string]any{}, f.cookie, "https://evil.test")
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("cross-origin creation", r.StatusCode)
	}
	id := f.create()
	c, resp, e := f.dial(id, "https://evil.test", f.cookie)
	if c != nil {
		c.Close()
	}
	if e == nil || resp.StatusCode != 403 {
		t.Fatal("cross-origin websocket accepted")
	}
	if resp != nil {
		resp.Body.Close()
	}
	r = f.call("/login", map[string]string{"username": "admin", "password": "terminal-test-password"}, nil, "")
	other := r.Cookies()[0]
	r.Body.Close()
	c, resp, e = f.dial(id, "https://console.test", other)
	if c != nil {
		c.Close()
	}
	if e == nil || resp.StatusCode != 404 {
		t.Fatal("different login session stole terminal")
	}
	if resp != nil {
		resp.Body.Close()
	}
	_ = f.create()
	r = f.call("/nodes/"+f.id+"/terminal", map[string]any{}, f.cookie, "https://console.test")
	r.Body.Close()
	if r.StatusCode != 409 {
		t.Fatal("session limit bypass")
	}
}
func TestNativeAgentRevocationClosesSession(t *testing.T) {
	f := newTerminalFixture(t, false)
	id := f.create()
	c, _, e := f.dial(id, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	readUntil(t, c, "ready")
	r := f.call("/nodes/"+f.id+"/agent-revoke", map[string]any{}, f.cookie, "")
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	waitAudit(t, f.s, id, "agent_revoked")
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	for {
		_, _, e = c.ReadMessage()
		if e != nil {
			break
		}
	}
	identity, e := f.s.identity(f.id)
	if e != nil || !identity.Revoked {
		t.Fatal("revocation not persisted")
	}
	if e = f.s.enroll(f.id, identity.PublicKey); e == nil {
		t.Fatal("revoked identity re-enrolled")
	}
}
func TestAgentChallengeReplayRejected(t *testing.T) {
	f := newTerminalFixture(t, false)
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	idKey, e := os.ReadFile(f.agent.IdentityFile)
	if e != nil {
		t.Fatal(e)
	}
	var identity edge.Identity
	_ = json.Unmarshal(idKey, &identity)
	seed, _ := base64.StdEncoding.DecodeString(identity.Seed)
	key = ed25519.NewKeyFromSeed(seed)
	d := websocket.Dialer{TLSClientConfig: f.server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}
	c, _, e := d.Dial("wss"+strings.TrimPrefix(f.server.URL, "https")+"/agent/control", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var challenge wire.Message
	if e = c.ReadJSON(&challenge); e != nil {
		t.Fatal(e)
	}
	sig := ed25519.Sign(key, wire.AuthMessage(f.id, strings.Repeat("0", 64)))
	_ = c.WriteJSON(wire.Message{Type: "auth", Serial: f.id, Signature: base64.StdEncoding.EncodeToString(sig)})
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if e = c.ReadJSON(&challenge); e == nil {
		t.Fatal("signature for another challenge accepted")
	}
}

func TestSlowBrowserCannotKeepSessionUnbounded(t *testing.T) {
	f := newTerminalFixture(t, false, 1)
	id := f.create()
	c, _, e := f.dial(id, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		kind, _, e := c.ReadMessage()
		if e != nil {
			t.Fatal(e)
		}
		if kind == websocket.BinaryMessage {
			break
		}
	} // Deliberately do not acknowledge output.
	waitAudit(t, f.s, id, "duration_limit")
	_ = f.create() // Capacity is released even with a blocked output writer.
}
func TestLogoutClosesNativeTerminal(t *testing.T) {
	f := newTerminalFixture(t, false)
	id := f.create()
	c, _, e := f.dial(id, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	readUntil(t, c, "ready")
	r := f.call("/logout", map[string]any{}, f.cookie, "")
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	waitAudit(t, f.s, id, "logout")
}
func TestAgentAutomaticallyReconnects(t *testing.T) {
	f := newTerminalFixture(t, false)
	f.a.gateway.Disconnect(f.id)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		_ = f.s.db.QueryRow(`SELECT count(*) FROM security_audit WHERE serial=? AND action='agent_authenticated'`, f.id).Scan(&count)
		if count >= 2 && f.a.gateway.Statuses()[f.id].Online {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("agent did not reconnect")
}

// Optional fixture for a real-browser test. It never connects to production nodes.
func TestNativeTerminalBrowserFixture(t *testing.T) {
	path := os.Getenv("NODEHUB_BROWSER_FIXTURE")
	if path == "" {
		t.Skip("optional browser fixture")
	}
	f := newTerminalFixture(t, false)
	b, _ := json.Marshal(map[string]string{"url": f.server.URL, "serial": f.id, "username": "admin", "password": "terminal-test-password"})
	if e := os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	defer os.Remove(path)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(path + ".done"); e == nil {
			os.Remove(path + ".done")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("browser fixture timed out")
}

func TestParallelNativeTerminalsRemainIndependent(t *testing.T) {
	f := newTerminalFixture(t, false)
	firstID, secondID := f.create(), f.create()
	first, _, e := f.dial(firstID, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, _, e := f.dial(secondID, "https://console.test", f.cookie)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	readUntil(t, first, "ready")
	readUntil(t, second, "ready")
	_ = first.WriteJSON(map[string]any{"type": "input", "data": "first-only\r"})
	readUntil(t, first, "first-only")
	_ = first.Close()
	waitAudit(t, f.s, firstID, "")
	_ = second.WriteJSON(map[string]any{"type": "input", "data": "second-still-open\r"})
	readUntil(t, second, "second-still-open")
}
func TestAuditStartCannotReopenEndedSession(t *testing.T) {
	s := testStore(t)
	record := remote.Record{ID: "test-session", Serial: "0123456789AB", Actor: "admin", Created: now()}
	if e := s.audit(record); e != nil {
		t.Fatal(e)
	}
	ended := record
	ended.Ended = now()
	ended.Reason = "logout"
	if e := s.audit(ended); e != nil {
		t.Fatal(e)
	}
	record.Started = now()
	if e := s.audit(record); e != nil {
		t.Fatal(e)
	}
	var end, reason string
	_ = s.db.QueryRow(`SELECT ended_at,reason FROM terminal_audit WHERE id=?`, record.ID).Scan(&end, &reason)
	if end == "" || reason != "logout" {
		t.Fatal("late start event reopened ended audit")
	}
}

func TestAutomaticSSHNativeTerminalRoundTrip(t *testing.T) {
	f := newTerminalFixtureMode(t, false, true)
	if len(f.a.targets) != 0 {
		t.Fatal("fixture unexpectedly uses manual targets")
	}
	id := f.create()
	c, _, err := f.dial(id, "https://console.test", f.cookie)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	readUntil(t, c, "ready")
	if err = c.WriteJSON(map[string]any{"type": "input", "data": "automatic SSH ready\r"}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "automatic SSH ready")
	_ = c.Close()
	waitAudit(t, f.s, id, "")
}
