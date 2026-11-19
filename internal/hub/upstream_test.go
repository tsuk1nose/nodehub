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
		_ = c.WriteJSON(map[string]any{"action": "nodes", "responseid": "nodehub", "nodes": map[string]any{"mesh//group": []any{map[string]any{"_id": "node//device", "name": id, "conn": 1, "ip": "8.8.4.4"}}}})
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
	if n.MeshID != "node//device" || n.MeshState != "online" || n.PublicIP != "8.8.4.4" {
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

func TestMeshPublicIP(t *testing.T) {
	for _, value := range []string{"", "invalid", "192.168.1.2", "10.0.0.1", "100.64.0.2", "127.0.0.1", "::1", "fe80::1", "fc00::1", "224.0.0.1", "::ffff:192.168.1.2"} {
		if got := meshPublicIP(value); got != "" {
			t.Errorf("non-public address accepted: %s", value)
		}
	}
	for _, value := range []string{"8.8.4.4", "2606:4700:4700::1111"} {
		if meshPublicIP(value) != value {
			t.Errorf("public IP lost: %s", value)
		}
	}
	if meshPublicIP("::ffff:8.8.4.4") != "8.8.4.4" {
		t.Fatal("mapped IPv4 not normalized")
	}
}

func TestHeadscaleTaggedNodeBinding(t *testing.T) {
	for _, tt := range []struct {
		name                        string
		tags                        []string
		owner, pak, serial, nodeKey string
		ok                          bool
	}{
		{"tag owner without user", []string{"tag:edge", "tag:site"}, "", "77", "", "", true},
		{"synthetic tag owner", []string{"tag:site", "tag:edge"}, "2147455555", "77", "", "", true},
		{"foreign preauth", []string{"tag:edge", "tag:site"}, "", "78", "", "", false},
		{"missing tag", []string{"tag:edge"}, "", "77", "", "", false},
		{"extra tag", []string{"tag:edge", "tag:site", "tag:admin"}, "", "77", "", "", false},
		{"untagged expected user", nil, "7", "77", "", "", false},
		{"wrong serial", []string{"tag:edge", "tag:site"}, "", "77", "OTHER", "", false},
		{"wrong node key", []string{"tag:edge", "tag:site"}, "", "77", "", strings.Repeat("b", 64), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := testStore(t)
			_, token, _ := s.NewToken("tagged", "", 1)
			id, err := s.Register(context.Background(), token, randomHex(32), randomHex(32))
			if err != nil {
				t.Fatal(err)
			}
			key := strings.Repeat("a", 64)
			nodeName := id
			if tt.serial != "" {
				nodeName = tt.serial
			}
			nodeKey := key
			if tt.nodeKey != "" {
				nodeKey = tt.nodeKey
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/preauthkey":
					reply(w, 200, map[string]any{"preAuthKey": map[string]string{"id": "77", "key": "issued-secret"}})
				case "/api/v1/node":
					reply(w, 200, map[string]any{"nodes": []any{map[string]any{"id": "42", "name": strings.ToLower(nodeName), "nodeKey": "nodekey:" + nodeKey, "tags": tt.tags, "user": map[string]string{"id": tt.owner}, "preAuthKey": map[string]string{"id": tt.pak, "key": "***"}}}})
				}
			}))
			defer srv.Close()
			c := Config{}
			c.Headscale.Enabled = true
			c.Headscale.InternalURL = srv.URL
			c.Headscale.UserID = "7"
			c.Headscale.Tags = []string{"tag:edge", "tag:site"}
			u := NewUpstream(c, s)
			if _, err = u.Preauth(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			err = u.BindHead(context.Background(), id, key)
			if (err == nil) != tt.ok {
				t.Fatalf("binding error %v; want success=%v", err, tt.ok)
			}
			if tt.ok {
				if err = u.BindHead(context.Background(), id, key); err != nil {
					t.Fatal("retry failed", err)
				}
			}
		})
	}
}

func TestHeadscaleUntaggedOwnershipRemainsStrict(t *testing.T) {
	var n HeadNode
	n.User.ID = "8"
	if headOwnershipMatches(n, "7", nil) {
		t.Fatal("foreign user accepted")
	}
	n.User.ID = "7"
	n.Tags = []string{"tag:edge"}
	if headOwnershipMatches(n, "7", nil) {
		t.Fatal("unexpected tagged node accepted")
	}
}
