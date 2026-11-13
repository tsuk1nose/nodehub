package hub

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestUpstreamDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		want   string
	}{
		{x509.HostnameError{Host: "private.example"}, 0, "hostname mismatch"},
		{x509.UnknownAuthorityError{}, 0, "not trusted"},
		{x509.CertificateInvalidError{}, 0, "invalid or expired"},
		{tls.RecordHeaderError{}, 0, "TLS or plaintext"},
		{&net.DNSError{Name: "private.example"}, 0, "DNS lookup failed"},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), 0, "connection refused"},
		{&net.DNSError{IsTimeout: true}, 0, "DNS lookup failed"},
		{nil, 401, "HTTP 401"},
		{nil, 502, "HTTP 502"},
		{errors.New("secret-password"), 0, "connection failed"},
	} {
		// Wrapped errors must retain classification without leaking URL userinfo or hostnames.
		wrapped := tc.err
		if wrapped != nil {
			wrapped = &url.Error{Op: "Get", URL: "https://private-user:secret-password@private.example", Err: wrapped}
		}
		got := upstreamConnectionError("Upstream", wrapped, tc.status).Error()
		if !strings.Contains(got, tc.want) {
			t.Fatalf("want %q, got %q", tc.want, got)
		}
		for _, secret := range []string{"secret-password", "private-user", "private.example"} {
			if strings.Contains(got, secret) {
				t.Fatal("private detail leaked")
			}
		}
	}
	s := testStore(t)
	if err := s.SyncStatus("headscale", "error", false, "Headscale HTTP 401"); err != nil {
		t.Fatal(err)
	}
	states, err := s.Statuses()
	if err != nil || len(states) != 1 || states[0]["error"] != "Headscale HTTP 401" {
		t.Fatal(states, err)
	}
}
