package hub

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeadscaleProvisionAndBinding(t *testing.T) {
	s := testStore(t)
	_, token, _ := s.NewToken("init", "", 0)
	id, _ := s.Register(context.Background(), token, randomHex(32), randomHex(32))
	key := strings.Repeat("a", 64)
	issued := 0
	foreign := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer management-secret" {
			t.Error("missing upstream authentication")
		}
		switch r.URL.Path {
		case "/api/v1/preauthkey":
			issued++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["user"] != "7" || body["reusable"] != false {
				t.Error("invalid provisioning request")
			}
			reply(w, 200, map[string]any{"preAuthKey": map[string]string{"key": "issued-key"}})
		case "/api/v1/node":
			pak := "issued-key"
			if foreign {
				pak = "other-key"
			}
			reply(w, 200, map[string]any{"nodes": []any{map[string]any{"id": "42", "nodeKey": "nodekey:" + key, "name": strings.ToLower(id), "user": map[string]string{"id": "7"}, "preAuthKey": map[string]string{"key": pak}, "online": true, "ipAddresses": []string{"100.64.0.1"}}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer remote.Close()
	c := Config{PollSeconds: 60}
	c.Headscale.Enabled = true
	c.Headscale.InternalURL = remote.URL
	c.Headscale.APIKey = "management-secret"
	c.Headscale.UserID = "7"
	u := NewUpstream(c, s)
	for range 2 {
		v, e := u.Preauth(context.Background(), id)
		if e != nil || v != "issued-key" {
			t.Fatal(e)
		}
	}
	if issued != 1 {
		t.Fatal("retry issued duplicate key")
	}
	foreign = true
	if e := u.BindHead(context.Background(), id, key); e == nil {
		t.Fatal("bound a node registered with someone else's preauth key")
	}
	foreign = false
	if e := u.BindHead(context.Background(), id, key); e != nil {
		t.Fatal(e)
	}
	if e := u.BindHead(context.Background(), id, key); e != nil {
		t.Fatal("binding retry failed", e)
	}
	u.Sync(context.Background())
	n, _ := s.Node(id)
	if n.HeadscaleID != "42" || n.HeadscaleState != "online" || n.Addresses != "100.64.0.1" {
		t.Fatal(n)
	}
}
func TestMeshCentralProtocolAndBinding(t *testing.T) {
	s := testStore(t)
	_, token, _ := s.NewToken("init", "", 0)
	credential := randomHex(32)
	id, _ := s.Register(context.Background(), token, randomHex(32), credential)
	upgrader := websocket.Upgrader{}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-meshauth") != "YWRtaW4=,c2VjcmV0" {
			t.Error("unexpected MeshCentral authentication")
		}
		c, e := upgrader.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer c.Close()
		var request map[string]any
		if e = c.ReadJSON(&request); e != nil {
			return
		}
		if request["action"] != "nodes" || request["meshid"] != "mesh//group" {
			t.Error("invalid MeshCentral request")
		}
		_ = c.WriteJSON(map[string]any{"action": "serverinfo"})
		_ = c.WriteJSON(map[string]any{"action": "nodes", "responseid": "nodehub", "nodes": map[string]any{"mesh//group": []any{map[string]any{"_id": "node//device", "name": id, "conn": 1}}}})
	}))
	defer remote.Close()
	c := Config{PollSeconds: 60}
	c.MeshCentral.Enabled = true
	c.MeshCentral.InternalURL = "ws" + strings.TrimPrefix(remote.URL, "http")
	c.MeshCentral.Username = "admin"
	c.MeshCentral.Password = "secret"
	c.MeshCentral.MeshID = "mesh//group"
	server := NewServer(c, s)
	defer server.Close()
	endpoint := "/api/node/" + id + "/meshcentral-auto-bind"
	request := httptest.NewRequest("POST", endpoint, nil)
	request.Header.Set("X-NodeHub-Request", "1")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("automatic binding requires node credentials", response.Code)
	}
	request = httptest.NewRequest("POST", endpoint, nil)
	request.Header.Set("X-NodeHub-Request", "1")
	request.Header.Set("Authorization", "Bearer "+credential)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal("automatic binding failed", response.Code, response.Body.String())
	}
	bound, _ := s.Node(id)
	if bound.MeshID != "node//device" {
		t.Fatal("automatic binding did not persist", bound.MeshID)
	}
	u := NewUpstream(c, s)
	if e := u.BindMesh(context.Background(), id, "node//wrong"); e == nil {
		t.Fatal("wrong device accepted")
	}
	if e := u.BindMesh(context.Background(), id, "node//device"); e != nil {
		t.Fatal(e)
	}
	u.Sync(context.Background())
	n, _ := s.Node(id)
	if n.MeshID != "node//device" || n.MeshState != "online" {
		t.Fatal(n)
	}
}

func TestHeadscaleMaskedPreauthCompatibility(t *testing.T) {
	key := "hskey-auth-abcdefghijkl-secret"
	if !matchesIssuedPreauth(key, "hskey-auth-abcdefghijkl-***") {
		t.Fatal("masked key from 0.29.7 rejected")
	}
	for _, bad := range []string{"", "***", "hskey-auth-x-***", "hskey-auth-otherprefix1-***", "hskey-auth-abcdefghijkl-secret-other"} {
		if matchesIssuedPreauth(key, bad) {
			t.Fatalf("foreign or insufficient prefix accepted: %q", bad)
		}
	}
}
func TestHeadscalePreauthIDBinding(t *testing.T) {
	s := testStore(t)
	_, token, _ := s.NewToken("masked", "", 1)
	id, _ := s.Register(context.Background(), token, randomHex(32), randomHex(32))
	key := strings.Repeat("a", 64)
	upstreamID := "77"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/preauthkey":
			reply(w, 200, map[string]any{"preAuthKey": map[string]string{"id": "77", "key": "hskey-auth-abcdefghijkl-secret"}})
		case "/api/v1/node":
			reply(w, 200, map[string]any{"nodes": []any{map[string]any{"id": "42", "name": id, "nodeKey": "nodekey:" + key, "user": map[string]string{"id": "7"}, "preAuthKey": map[string]string{"id": upstreamID, "key": "hskey-auth-abcdefghijkl-***"}}}})
		}
	}))
	defer srv.Close()
	c := Config{}
	c.Headscale.Enabled = true
	c.Headscale.InternalURL = srv.URL
	c.Headscale.UserID = "7"
	u := NewUpstream(c, s)
	if _, e := u.Preauth(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	upstreamID = "78"
	if e := u.BindHead(context.Background(), id, key); e == nil {
		t.Fatal("foreign preauth ID accepted")
	}
	upstreamID = "77"
	if e := u.BindHead(context.Background(), id, key); e != nil {
		t.Fatal(e)
	}
}
