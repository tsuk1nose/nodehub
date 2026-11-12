package remote

import (
	"crypto/sha256"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"nodehub/internal/wire"
	"strings"
	"testing"
	"time"
)

func TestDataTicketSingleUseAndDeviceBinding(t *testing.T) {
	cfg := Config{Enabled: true}
	g := New(cfg, nil, Hooks{})
	d := &device{serial: "0123456789AB"}
	token := random()
	s := &session{id: random(), serial: d.serial, dev: d, created: time.Now(), ready: make(chan struct{}), done: make(chan struct{}), token: sha256.Sum256([]byte(token))}
	g.devices[d.serial] = d
	g.sessions[s.id] = s
	srv := httptest.NewServer(http.HandlerFunc(g.Data))
	defer srv.Close()
	connect := func(serial, secret string) *websocket.Conn {
		c, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if e != nil {
			t.Fatal(e)
		}
		_ = c.WriteJSON(wire.Message{Type: "data", Serial: serial, Session: s.id, Token: secret})
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		return c
	}
	c := connect("WRONGDEVICE1", token)
	if _, _, e := c.ReadMessage(); e == nil {
		t.Fatal("ticket accepted for another device")
	}
	c.Close()
	c = connect(d.serial, random())
	if _, _, e := c.ReadMessage(); e == nil {
		t.Fatal("wrong ticket accepted")
	}
	c.Close()
	first := connect(d.serial, token)
	defer first.Close()
	select {
	case <-s.ready:
	case <-time.After(time.Second):
		t.Fatal("valid ticket not accepted")
	}
	defer s.tunnel.Close()
	replay := connect(d.serial, token)
	defer replay.Close()
	if _, _, e := replay.ReadMessage(); e == nil {
		t.Fatal("data ticket replay accepted")
	}
}
