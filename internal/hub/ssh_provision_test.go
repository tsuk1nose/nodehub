package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSSHProvisionLifecycle(t *testing.T) {
	s := testStore(t)
	_, token, _ := s.NewToken("ssh", "", 0)
	credential := randomHex(32)
	serial, err := s.Register(context.Background(), token, randomHex(32), credential)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{}
	cfg.Terminal.Enabled = true
	cfg.Terminal.Defaults()
	a := NewServer(cfg, s)
	defer a.Close()
	call := func(path, credential string, body any) (int, string) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/node/"+serial+path, strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-NodeHub-Request", "1")
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	host := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer(t).PublicKey())))
	body := map[string]string{"host_key": host, "username": "root"}
	for _, step := range []struct {
		path, credential string
		code             int
	}{
		{"/ssh-provision", "wrong", 401},
		{"/ssh-provision", credential, 409}, // Agent must be enrolled first.
	} {
		if code, _ := call(step.path, step.credential, body); code != step.code {
			t.Fatalf("%s got %d", step.path, code)
		}
	}
	agentKey := signer(t).PublicKey().Marshal()
	// Enrollment accepts the raw Ed25519 public key, not SSH wire encoding.
	raw := agentKey[len(agentKey)-32:]
	if code, _ := call("/agent-enroll", credential, map[string]string{"public_key": base64.StdEncoding.EncodeToString(raw)}); code != 200 {
		t.Fatal(code)
	}
	if code, _ := call("/ssh-provision/confirm", credential, body); code != 409 {
		t.Fatal("confirmation created a record", code)
	}
	code, response := call("/ssh-provision", credential, body)
	if code != 200 {
		t.Fatal(code, response)
	}
	if strings.Contains(response, "PRIVATE KEY") {
		t.Fatal("private key leaked")
	}
	var provision struct{ Username, AuthorizedKey string }
	var result map[string]any
	_ = json.Unmarshal([]byte(response), &result)
	provision.Username = result["username"].(string)
	provision.AuthorizedKey = result["authorized_key"].(string)
	if provision.Username != sshLoginUser {
		t.Fatal(provision)
	}
	if _, err := a.sshTarget(serial); err == nil {
		t.Fatal("target available before confirmation")
	}
	_, again := call("/ssh-provision", credential, body)
	if response != again {
		t.Fatal("retry changed login key")
	}
	changed := map[string]string{"username": "root", "host_key": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer(t).PublicKey())))}
	if code, _ := call("/ssh-provision", credential, changed); code != 409 {
		t.Fatal("host key replacement accepted")
	}
	if code, _ := call("/ssh-provision/confirm", credential, body); code != 200 {
		t.Fatal(code)
	}
	target, err := a.sshTarget(serial)
	if err != nil {
		t.Fatal(err)
	}
	statusRequest := httptest.NewRequest("GET", "/api/agents", nil)
	statusResponse := httptest.NewRecorder()
	a.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != 200 || !strings.Contains(statusResponse.Body.String(), `"ssh_configured":true`) {
		t.Fatal("automatic readiness missing from UI API", statusResponse.Body.String())
	}
	if strings.Contains(statusResponse.Body.String(), "PRIVATE KEY") || strings.Contains(statusResponse.Body.String(), provision.AuthorizedKey) {
		t.Fatal("SSH material exposed in management status")
	}
	if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(target.Signer.PublicKey()))) != provision.AuthorizedKey {
		t.Fatal("login key mismatch")
	}
	if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(target.HostKey))) != host {
		t.Fatal("host pin mismatch")
	}
	// Runtime lookup must see provisioning immediately, without replacing the server.
	_, err = a.gateway.Create(serial, "external-admin", "admin", "local")
	if err == nil || err.Error() != "Agent is offline" {
		t.Fatalf("dynamic target was not found: %v", err)
	}
	// Existing maintenance targets keep working until the root installation is confirmed.
	if _, err = s.db.Exec(`UPDATE terminal_ssh SET username='nodehub-maint' WHERE serial=?`, serial); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("/ssh-provision", credential, body); code != 200 {
		t.Fatal(code)
	}
	pending, err := a.sshTarget(serial)
	if err != nil || pending.User != "nodehub-maint" {
		t.Fatal("legacy target changed before confirmation", err)
	}
	if code, _ := call("/ssh-provision/confirm", credential, body); code != 200 {
		t.Fatal(code)
	}
	migrated, err := a.sshTarget(serial)
	if err != nil || migrated.User != "root" || string(migrated.Signer.PublicKey().Marshal()) != string(target.Signer.PublicKey().Marshal()) {
		t.Fatal("root migration changed identity", err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err = s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	reloaded := NewServer(cfg, restored)
	defer reloaded.Close()
	restoredTarget, err := reloaded.sshTarget(serial)
	if err != nil || string(restoredTarget.Signer.PublicKey().Marshal()) != string(target.Signer.PublicKey().Marshal()) {
		t.Fatal("backup lost SSH identity", err)
	}
	if _, err = s.db.Exec(`UPDATE agent_identities SET revoked=1 WHERE serial=?`, serial); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("/ssh-provision", credential, body); code != 409 {
		t.Fatal("revoked identity provisioned")
	}
	if code, _ := call("/ssh-provision/confirm", credential, body); code != 409 {
		t.Fatal("revoked identity confirmed")
	}
	cfg.Terminal.Enabled = false
	disabled := NewServer(cfg, s)
	defer disabled.Close()
	r := httptest.NewRequest("POST", "/api/node/"+serial+"/ssh-provision", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+credential)
	r.Header.Set("X-NodeHub-Request", "1")
	w := httptest.NewRecorder()
	disabled.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("disabled provisioning accessible", w.Code)
	}
}

func TestSSHProvisionConcurrentAndIndependent(t *testing.T) {
	s := testStore(t)
	_, token, _ := s.NewToken("ssh", "", 0)
	register := func() string {
		serial, err := s.Register(context.Background(), token, randomHex(32), randomHex(32))
		if err != nil {
			t.Fatal(err)
		}
		raw := signer(t).PublicKey().Marshal()
		if err = s.enroll(serial, raw[len(raw)-32:]); err != nil {
			t.Fatal(err)
		}
		return serial
	}
	serial := register()
	host := string(ssh.MarshalAuthorizedKey(signer(t).PublicKey()))
	var wg sync.WaitGroup
	results := make(chan string, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := s.provisionSSH(serial, host)
			if err != nil {
				t.Error(err)
			}
			results <- key
		}()
	}
	wg.Wait()
	close(results)
	first := ""
	for key := range results {
		if first == "" {
			first = key
		}
		if key != first {
			t.Fatal("concurrent requests rotated key")
		}
	}
	second, err := s.provisionSSH(register(), host)
	if err != nil || first == second {
		t.Fatal("devices share login key", err)
	}
	for _, invalid := range []string{host + host, "restrict " + host, "not a key"} {
		if _, err = s.provisionSSH(serial, invalid); err == nil {
			t.Fatal("invalid host key accepted")
		}
	}
}
