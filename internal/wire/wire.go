// Package wire defines the versioned control messages and bounded binary tunnel.
package wire

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const Version = "nodehub-agent-v1"
const MaxChunk = 16 * 1024

type Message struct {
	Type      string `json:"type"`
	Serial    string `json:"serial,omitempty"`
	Challenge string `json:"challenge,omitempty"`
	Signature string `json:"signature,omitempty"`
	Session   string `json:"session,omitempty"`
	Token     string `json:"token,omitempty"`
	Info      *Info  `json:"info,omitempty"`
}
type Info struct {
	OSVersion     string `json:"os_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Version       string `json:"version"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

func AuthMessage(serial, challenge string) []byte {
	return []byte(Version + "\n" + serial + "\n" + challenge)
}

// Conn carries SSH's byte stream in bounded binary WebSocket messages.
// One reader and one writer are supported, as required by net.Conn.
type Conn struct {
	WS      *websocket.Conn
	reader  io.Reader
	writeMu sync.Mutex
}

func NewConn(ws *websocket.Conn) *Conn { ws.SetReadLimit(MaxChunk); return &Conn{WS: ws} }
func (c *Conn) Read(p []byte) (int, error) {
	for {
		if c.reader == nil {
			kind, r, e := c.WS.NextReader()
			if e != nil {
				return 0, e
			}
			if kind != websocket.BinaryMessage {
				return 0, errors.New("binary tunnel frame required")
			}
			c.reader = r
		}
		n, e := c.reader.Read(p)
		if e == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, e
	}
}
func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		n := min(len(p), MaxChunk)
		if e := c.WS.SetWriteDeadline(time.Now().Add(10 * time.Second)); e != nil {
			return total, e
		}
		if e := c.WS.WriteMessage(websocket.BinaryMessage, p[:n]); e != nil {
			return total, e
		}
		total += n
		p = p[n:]
	}
	return total, nil
}
func (c *Conn) Close() error         { return c.WS.Close() }
func (c *Conn) LocalAddr() net.Addr  { return c.WS.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.WS.RemoteAddr() }
func (c *Conn) SetDeadline(t time.Time) error {
	if e := c.SetReadDeadline(t); e != nil {
		return e
	}
	return c.SetWriteDeadline(t)
}
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.WS.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.WS.SetWriteDeadline(t) }
