// Package edge implements Node Hub's outbound-only, non-root SSH relay agent.
package edge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"nodehub/internal/wire"
)

const Version = "0.2.0"

type Config struct {
	Server              string `json:"server"`
	Serial              string `json:"serial"`
	IdentityFile        string `json:"identity_file"`
	CAFile              string `json:"ca_file"`
	SSHPort             int    `json:"ssh_port"`
	HeartbeatSeconds    int    `json:"heartbeat_seconds"`
	ReconnectMaxSeconds int    `json:"reconnect_max_seconds"`
	MaxSessions         int    `json:"max_sessions"`
}
type Identity struct {
	Serial string `json:"serial"`
	Server string `json:"server"`
	Seed   string `json:"seed"`
}
type Agent struct {
	cfg Config
	id  Identity
	key ed25519.PrivateKey
	tls *tls.Config
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	if c.SSHPort == 0 {
		c.SSHPort = 22
	}
	if c.HeartbeatSeconds == 0 {
		c.HeartbeatSeconds = 30
	}
	if c.ReconnectMaxSeconds == 0 {
		c.ReconnectMaxSeconds = 60
	}
	if c.MaxSessions == 0 {
		c.MaxSessions = 2
	}
	if c.SSHPort < 1 || c.SSHPort > 65535 || c.HeartbeatSeconds < 1 || c.HeartbeatSeconds > 300 || c.ReconnectMaxSeconds < 1 || c.ReconnectMaxSeconds > 3600 || c.MaxSessions < 1 || c.MaxSessions > 32 || c.IdentityFile == "" {
		return c, errors.New("invalid agent limits or identity path")
	}
	u, e := url.Parse(c.Server)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return c, errors.New("server must be a trusted HTTPS URL")
	}
	return c, nil
}
func tlsConfig(c Config) (*tls.Config, error) {
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if c.CAFile != "" {
		b, e := os.ReadFile(c.CAFile)
		if e != nil {
			return nil, e
		}
		if !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("invalid CA file")
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, nil
}
func saveIdentity(path string, id Identity) error {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".identity-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e != nil {
		f.Close()
		return e
	}
	if e = json.NewEncoder(f).Encode(id); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func loadIdentity(path string) (Identity, ed25519.PrivateKey, error) {
	var id Identity
	st, e := os.Stat(path)
	if e != nil {
		return id, nil, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return id, nil, errors.New("identity must be a regular file with mode 0600 or stricter")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return id, nil, e
	}
	if e = json.Unmarshal(b, &id); e != nil {
		return id, nil, e
	}
	seed, e := base64.StdEncoding.DecodeString(id.Seed)
	if e != nil || len(seed) != ed25519.SeedSize || len(id.Serial) != 12 || strings.Trim(id.Serial, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return id, nil, errors.New("invalid agent identity")
	}
	return id, ed25519.NewKeyFromSeed(seed), nil
}
func Enroll(ctx context.Context, c Config, bootstrapPath string) error {
	if e := os.MkdirAll(filepath.Dir(c.IdentityFile), 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(c.IdentityFile+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return errors.New("agent enrollment already in progress")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	var bootstrap struct {
		Server     string `json:"server"`
		Serial     string `json:"serial"`
		Credential string `json:"credential"`
	}
	b, e := os.ReadFile(bootstrapPath)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &bootstrap); e != nil {
		return e
	}
	if len(bootstrap.Serial) != 12 || strings.Trim(bootstrap.Serial, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" || len(bootstrap.Credential) != 64 || strings.TrimRight(bootstrap.Server, "/") != strings.TrimRight(c.Server, "/") || (c.Serial != "" && c.Serial != bootstrap.Serial) {
		return errors.New("bootstrap identity or server mismatch")
	}
	id, key, e := loadIdentity(c.IdentityFile)
	if errors.Is(e, os.ErrNotExist) {
		seed := make([]byte, ed25519.SeedSize)
		if _, e = rand.Read(seed); e != nil {
			return e
		}
		id = Identity{bootstrap.Serial, strings.TrimRight(c.Server, "/"), base64.StdEncoding.EncodeToString(seed)}
		if e = saveIdentity(c.IdentityFile, id); e != nil {
			return e
		}
		key = ed25519.NewKeyFromSeed(seed)
	} else if e != nil {
		return e
	}
	if id.Serial != bootstrap.Serial || id.Server != strings.TrimRight(c.Server, "/") {
		return errors.New("existing identity mismatch; never overwrite device keys")
	}
	tlsCfg, e := tlsConfig(c)
	if e != nil {
		return e
	}
	transport := &http.Transport{TLSClientConfig: tlsCfg}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	payload, _ := json.Marshal(map[string]string{"public_key": base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))})
	r, e := http.NewRequestWithContext(ctx, "POST", id.Server+"/api/node/"+id.Serial+"/agent-enroll", strings.NewReader(string(payload)))
	if e != nil {
		return e
	}
	r.Header.Set("Authorization", "Bearer "+bootstrap.Credential)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-NodeHub-Request", "1")
	resp, e := client.Do(r)
	if e != nil {
		return errors.New("agent enrollment TLS or network failure")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent enrollment rejected (HTTP %d)", resp.StatusCode)
	}
	return nil
}
func New(c Config) (*Agent, error) {
	id, key, e := loadIdentity(c.IdentityFile)
	if e != nil {
		return nil, e
	}
	if id.Server != strings.TrimRight(c.Server, "/") || (c.Serial != "" && c.Serial != id.Serial) {
		return nil, errors.New("agent identity belongs to a different server or serial")
	}
	t, e := tlsConfig(c)
	if e != nil {
		return nil, e
	}
	return &Agent{c, id, key, t}, nil
}
func (a *Agent) dial(ctx context.Context, path string) (*websocket.Conn, error) {
	u, _ := url.Parse(a.cfg.Server)
	u.Scheme = "wss"
	u.Path = strings.TrimRight(u.Path, "/") + path
	d := websocket.Dialer{TLSClientConfig: a.tls, HandshakeTimeout: 10 * time.Second}
	c, resp, e := d.DialContext(ctx, u.String(), nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return c, e
}
func info() wire.Info {
	uptime := int64(0)
	if b, e := os.ReadFile("/proc/uptime"); e == nil {
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			v, _ := strconv.ParseFloat(fields[0], 64)
			uptime = int64(v)
		}
	}
	osVersion := ""
	if b, e := os.ReadFile("/etc/os-release"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				v := strings.TrimPrefix(line, "PRETTY_NAME=")
				if decoded, e := strconv.Unquote(v); e == nil {
					v = decoded
				}
				osVersion = v
				break
			}
		}
	}
	if len(osVersion) > 128 {
		osVersion = osVersion[:128]
	}
	return wire.Info{OSVersion: osVersion, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: Version, UptimeSeconds: uptime}
}
func (a *Agent) connect(ctx context.Context) error {
	c, e := a.dial(ctx, "/agent/control")
	if e != nil {
		return errors.New("control connection failed")
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	c.SetReadLimit(8192)
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var m wire.Message
	if e = c.ReadJSON(&m); e != nil || m.Type != "challenge" || len(m.Challenge) != 64 {
		return errors.New("invalid challenge")
	}
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if e = c.WriteJSON(wire.Message{Type: "auth", Serial: a.id.Serial, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(a.key, wire.AuthMessage(a.id.Serial, m.Challenge)))}); e != nil {
		return e
	}
	if e = c.ReadJSON(&m); e != nil || m.Type != "authenticated" {
		return errors.New("device authentication rejected")
	}
	child, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() { cancel(); _ = c.Close(); wg.Wait() }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		send := func() error {
			v := info()
			_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			return c.WriteJSON(wire.Message{Type: "heartbeat", Info: &v})
		}
		if send() != nil {
			_ = c.Close()
			return
		}
		t := time.NewTicker(time.Duration(a.cfg.HeartbeatSeconds) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-child.Done():
				return
			case <-t.C:
				if send() != nil {
					_ = c.Close()
					return
				}
			}
		}
	}()
	slots := make(chan struct{}, a.cfg.MaxSessions)
	active := map[string]bool{}
	var mu sync.Mutex
	for {
		_ = c.SetReadDeadline(time.Now().Add(time.Duration(max(90, a.cfg.HeartbeatSeconds*3)) * time.Second))
		if e = c.ReadJSON(&m); e != nil {
			return errors.New("control disconnected")
		}
		switch m.Type {
		case "heartbeat_ack":
		case "open":
			if len(m.Session) != 64 || len(m.Token) != 64 {
				return errors.New("invalid session request")
			}
			mu.Lock()
			if active[m.Session] {
				mu.Unlock()
				continue
			}
			select {
			case slots <- struct{}{}:
				active[m.Session] = true
			default:
				mu.Unlock()
				continue
			}
			mu.Unlock()
			request := m
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots; mu.Lock(); delete(active, request.Session); mu.Unlock() }()
				a.relay(child, request)
			}()
		default:
			return errors.New("unknown control message")
		}
	}
}
func (a *Agent) relay(ctx context.Context, m wire.Message) {
	c, e := a.dial(ctx, "/agent/data")
	if e != nil {
		return
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if e = c.WriteJSON(wire.Message{Type: "data", Serial: a.id.Serial, Session: m.Session, Token: m.Token}); e != nil {
		return
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	local, e := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(a.cfg.SSHPort)))
	if e != nil {
		return
	}
	defer local.Close()
	stopLocal := context.AfterFunc(ctx, func() { _ = local.Close() })
	defer stopLocal()
	tunnel := wire.NewConn(c)
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(tunnel, local); done <- struct{}{} }()
	go func() { _, _ = io.Copy(local, tunnel); done <- struct{}{} }()
	<-done
	_ = local.Close()
	_ = tunnel.Close()
	<-done
}
func (a *Agent) Run(ctx context.Context) error {
	delay := time.Second
	limit := time.Duration(a.cfg.ReconnectMaxSeconds) * time.Second
	for {
		started := time.Now()
		_ = a.connect(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(started) > 90*time.Second {
			delay = time.Second
		}
		slog.Warn("agent disconnected; reconnecting")
		n, e := rand.Int(rand.Reader, big.NewInt(max(1, int64(delay/2))))
		if e != nil {
			return e
		}
		t := time.NewTimer(delay/2 + time.Duration(n.Int64()))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		delay = min(delay*2, limit)
	}
}
