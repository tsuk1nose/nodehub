package remote

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
	"nodehub/internal/wire"
)

type browserMessage struct {
	Type  string `json:"type"`
	Data  string `json:"data"`
	Cols  int    `json:"cols"`
	Rows  int    `json:"rows"`
	Bytes int    `json:"bytes"`
}

func (s *session) write(kind int, p []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.browser.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.browser.WriteMessage(kind, p)
}
func (s *session) status(message string) {
	b, _ := json.Marshal(map[string]string{"type": "status", "message": message})
	_ = s.write(websocket.TextMessage, b)
}
func (g *Gateway) Browser(w http.ResponseWriter, r *http.Request, id, owner string) {
	if !g.Origin(r) {
		http.Error(w, "origin denied", 403)
		return
	}
	g.mu.Lock()
	s := g.sessions[id]
	if s == nil || s.owner != owner {
		g.mu.Unlock()
		http.Error(w, "session unavailable", 404)
		return
	}
	s.mu.Lock()
	g.mu.Unlock()
	if s.claimed {
		s.mu.Unlock()
		http.Error(w, "already connected", 409)
		return
	}
	select {
	case <-s.done:
		s.mu.Unlock()
		http.Error(w, "session closed", 410)
		return
	default:
	}
	s.claimed = true
	s.mu.Unlock()
	c, e := (&websocket.Upgrader{CheckOrigin: g.Origin}).Upgrade(w, r, nil)
	if e != nil {
		g.closeSession(s, "browser_upgrade_failed")
		return
	}
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		_ = c.Close()
		return
	default:
	}
	s.browser = c
	s.mu.Unlock()
	reason := "session_closed"
	defer func() { g.closeSession(s, reason) }()
	c.SetReadLimit(16 << 10)
	token := random()
	s.mu.Lock()
	s.token = sha256.Sum256([]byte(token))
	s.mu.Unlock()
	if e = s.dev.send(wire.Message{Type: "open", Session: s.id, Token: token}); e != nil {
		reason = "agent_unavailable"
		return
	}
	select {
	case <-s.done:
		return
	case <-time.After(15 * time.Second):
		s.status("Device data connection timed out")
		reason = "data_timeout"
		return
	case <-s.ready:
	}
	s.mu.Lock()
	tunnel := s.tunnel
	s.mu.Unlock()
	target := s.target
	cfg := &ssh.ClientConfig{User: target.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(target.Signer)}, HostKeyCallback: ssh.FixedHostKey(target.HostKey), HostKeyAlgorithms: hostKeyAlgorithms(target.HostKey), Timeout: 15 * time.Second}
	cc, ch, requests, e := ssh.NewClientConn(tunnel, "127.0.0.1:22", cfg)
	if e != nil {
		slog.Warn("terminal SSH handshake failed", "serial", s.serial, "error", e)
		s.status("SSH authentication or host key verification failed; check the configuration")
		reason = "ssh_handshake_failed"
		return
	}
	_ = tunnel.SetReadDeadline(time.Now().Add(15 * time.Second))
	client := ssh.NewClient(cc, ch, requests)
	s.mu.Lock()
	select {
	case <-s.done:
		s.mu.Unlock()
		_ = client.Close()
		return
	default:
	}
	s.sshCloser = client
	s.mu.Unlock()
	sh, e := client.NewSession()
	if e != nil {
		reason = "ssh_session_failed"
		return
	}
	defer sh.Close()
	stdin, e := sh.StdinPipe()
	if e != nil {
		reason = "ssh_stdin_failed"
		return
	}
	ack := make(chan int, 1)
	input := make(chan browserMessage, 8)
	readerDone := make(chan string, 1)
	go func() { readerDone <- readBrowser(s, c, ack, input) }()
	go func() {
		for {
			select {
			case <-s.done:
				return
			case m := <-input:
				var err error
				if m.Type == "input" {
					_, err = io.WriteString(stdin, m.Data)
				} else {
					err = sh.WindowChange(m.Rows, m.Cols)
				}
				if err != nil {
					g.closeSession(s, "ssh_input_failed")
					return
				}
			}
		}
	}()
	output := &terminalOutput{s: s, ack: ack}
	sh.Stdout = output
	sh.Stderr = output
	if e = sh.RequestPty("xterm-256color", 24, 80, ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); e != nil {
		reason = "pty_failed"
		return
	}
	if e = sh.Shell(); e != nil {
		reason = "shell_failed"
		return
	}
	_ = tunnel.SetReadDeadline(time.Time{})
	s.mu.Lock()
	s.started = time.Now().UTC().Format(time.RFC3339)
	record := s.record("", "")
	s.mu.Unlock()
	if e = g.hooks.Audit(record); e != nil {
		reason = "audit_failed"
		return
	}
	s.status("Connected")
	exit := make(chan error, 1)
	go func() { exit <- sh.Wait() }()
	select {
	case reason = <-readerDone:
	case <-s.done:
		return
	case e = <-exit:
		reason = "shell_exit"
		if e != nil {
			reason = "shell_error"
		}
		s.status("Terminal exited")
	}
}
func readBrowser(s *session, c *websocket.Conn, ack chan<- int, input chan<- browserMessage) string {
	for {
		kind, p, e := c.ReadMessage()
		if e != nil {
			return "browser_disconnected"
		}
		if kind != websocket.TextMessage {
			return "invalid_browser_frame"
		}
		var m browserMessage
		if json.Unmarshal(p, &m) != nil {
			return "invalid_browser_message"
		}
		switch m.Type {
		case "ack":
			if m.Bytes < 1 || m.Bytes > wire.MaxChunk {
				return "invalid_ack"
			}
			select {
			case ack <- m.Bytes:
			default:
				return "invalid_ack"
			}
		case "input":
			if len(m.Data) > 4096 {
				return "input_limit"
			}
			s.last.Store(time.Now().UnixNano())
			select {
			case input <- m:
			default:
				return "input_limit"
			}
		case "resize":
			if m.Cols < 2 || m.Cols > 500 || m.Rows < 1 || m.Rows > 300 {
				return "invalid_resize"
			}
			s.last.Store(time.Now().UnixNano())
			select {
			case input <- m:
			default:
				return "input_limit"
			}
		default:
			return "invalid_browser_message"
		}
		select {
		case <-s.done:
			return "session_closed"
		default:
		}
	}
}

// Stop-and-wait output acknowledgements bound the browser queue to one 16 KiB
// frame. This favors predictable memory over bulk-transfer throughput.
type terminalOutput struct {
	s   *session
	ack <-chan int
}

func (o *terminalOutput) Write(p []byte) (int, error) {
	// SSH stdout and stderr may write concurrently. Serialize a whole ack cycle.
	o.s.outputMu.Lock()
	defer o.s.outputMu.Unlock()
	total := 0
	for len(p) > 0 {
		n := min(len(p), wire.MaxChunk)
		if e := o.s.write(websocket.BinaryMessage, p[:n]); e != nil {
			return total, e
		}
		timer := time.NewTimer(20 * time.Second)
		select {
		case count := <-o.ack:
			timer.Stop()
			if count != n {
				return total, errors.New("output acknowledgement mismatch")
			}
		case <-o.s.done:
			timer.Stop()
			return total, io.ErrClosedPipe
		case <-timer.C:
			return total, errors.New("slow browser")
		}
		o.s.last.Store(time.Now().UnixNano())
		p = p[n:]
		total += n
	}
	return total, nil
}

// Match negotiation to the pinned key when sshd offers several host keys.
func hostKeyAlgorithms(key ssh.PublicKey) []string {
	if key.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
	}
	return []string{key.Type()}
}
