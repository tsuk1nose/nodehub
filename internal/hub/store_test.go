package hub

import (
	"context"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestRegistrationRetryAndQuota(t *testing.T) {
	s := testStore(t)
	_, token, e := s.NewToken("single", "", 1)
	if e != nil {
		t.Fatal(e)
	}
	request, credential := randomHex(32), randomHex(32)
	id, e := s.Register(context.Background(), token, request, credential)
	if e != nil {
		t.Fatal(e)
	}
	if len(id) != 12 || strings.Trim(id, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		t.Fatalf("bad serial %q", id)
	}
	again, e := s.Register(context.Background(), "", request, credential)
	if e != nil || again != id {
		t.Fatalf("retry failed: %v", e)
	}
	if _, e = s.Register(context.Background(), token, randomHex(32), randomHex(32)); e != ErrUnauthorized {
		t.Fatalf("quota bypass: %v", e)
	}
	if _, e = s.Register(context.Background(), token, request, randomHex(32)); e != ErrUnauthorized {
		t.Fatalf("credential replacement allowed: %v", e)
	}
	ts, _ := s.Tokens()
	if ts[0].Uses != 1 {
		t.Fatal("retry consumed quota")
	}
	if !s.AuthenticateNode(id, credential) || s.AuthenticateNode(id, token) {
		t.Fatal("authentication mismatch")
	}
}
func TestConcurrentQuota(t *testing.T) {
	s := testStore(t)
	_, token, _ := s.NewToken("one", "", 1)
	ch := make(chan error, 10)
	for range 10 {
		go func() { _, e := s.Register(context.Background(), token, randomHex(32), randomHex(32)); ch <- e }()
	}
	success := 0
	for range 10 {
		if <-ch == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("got %d registrations", success)
	}
}
func TestExpiredAndRevokedTokens(t *testing.T) {
	s := testStore(t)
	tok, key, _ := s.NewToken("test", "", 0)
	_, _ = s.db.Exec(`UPDATE tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), tok.ID)
	if _, e := s.Register(context.Background(), key, randomHex(32), randomHex(32)); e != ErrUnauthorized {
		t.Fatal(e)
	}
	_, _ = s.db.Exec(`UPDATE tokens SET expires_at='',revoked=1 WHERE id=?`, tok.ID)
	if _, e := s.Register(context.Background(), key, randomHex(32), randomHex(32)); e != ErrUnauthorized {
		t.Fatal(e)
	}
}
func TestSyncFailurePreservesState(t *testing.T) {
	s := testStore(t)
	_, key, _ := s.NewToken("test", "", 0)
	id, _ := s.Register(context.Background(), key, randomHex(32), randomHex(32))
	_, _ = s.db.Exec(`UPDATE nodes SET headscale_id='1',headscale_state='online' WHERE serial=?`, id)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer bad.Close()
	c := Config{PollSeconds: 60}
	c.Headscale.Enabled = true
	c.Headscale.InternalURL = bad.URL
	u := NewUpstream(c, s)
	u.Sync(context.Background())
	n, _ := s.Node(id)
	if n.HeadscaleState != "online" {
		t.Fatal("API failure marked node offline")
	}
	ss, _ := s.Statuses()
	if ss[0]["state"] != "error" {
		t.Fatal(ss)
	}
}
func TestAdminAuthenticationAndCSRF(t *testing.T) {
	s := testStore(t)
	h, _ := bcrypt.GenerateFromPassword([]byte("test-password-long"), bcrypt.MinCost)
	c := Config{PollSeconds: 60}
	c.Auth.Enabled = true
	c.Auth.Username = "admin"
	c.Auth.PasswordHash = string(h)
	c.Auth.SessionHours = 12
	a := NewServer(c, s)
	call := func(method, path, body string, header bool, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if header {
			r.Header.Set("X-NodeHub-Request", "1")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/api/nodes", "", false, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/api/login", `{"username":"admin","password":"test-password-long"}`, false, nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := call("POST", "/api/login", `{"username":"admin","password":"test-password-long"}`, true, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if w = call("GET", "/api/nodes", "", false, cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	_ = call("POST", "/api/logout", `{}`, true, cookie)
	if w = call("GET", "/api/nodes", "", false, cookie); w.Code != 401 {
		t.Fatal("logout did not invalidate session")
	}
}
func TestBackup(t *testing.T) {
	s := testStore(t)
	_, _, _ = s.NewToken("persist", "", 0)
	path := filepath.Join(t.TempDir(), "backup.db")
	if e := s.Backup(path); e != nil {
		t.Fatal(e)
	}
	copy, e := OpenStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer copy.Close()
	ts, e := copy.Tokens()
	if e != nil || len(ts) != 1 {
		t.Fatal("inconsistent backup", e)
	}
}
