package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestUpstreamTLSPolicyIsolation(t *testing.T) {
	global := http.DefaultTransport.(*http.Transport)
	original := global.TLSClientConfig
	for _, head := range []bool{false, true} {
		for _, mesh := range []bool{false, true} {
			cfg := Config{}
			cfg.Headscale.TLSSkipVerify = head
			cfg.MeshCentral.TLSSkipVerify = mesh
			upstream := NewUpstream(cfg, nil)
			transport := upstream.http.Transport.(*http.Transport)
			if transport == global || transport.TLSClientConfig.InsecureSkipVerify != head {
				t.Fatal("Headscale TLS policy is not scoped")
			}
			dialer := upstream.meshDialer()
			if dialer.TLSClientConfig.InsecureSkipVerify != mesh {
				t.Fatal("MeshCentral TLS policy is not scoped")
			}
			if global.TLSClientConfig != original {
				t.Fatal("global transport mutated")
			}
			fresh := NewUpstream(Config{}, nil)
			if fresh.http.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify || fresh.meshDialer().TLSClientConfig.InsecureSkipVerify {
				t.Fatal("default verification changed")
			}
			transport.CloseIdleConnections()
		}
	}
}

// Exercise real TLS handshakes in memory without opening a listening port.
func TestUpstreamTLSHandshakePolicy(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"certificate.example"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}}
	for _, skip := range []bool{false, true} {
		cfg := Config{}
		cfg.Headscale.TLSSkipVerify = skip
		cfg.MeshCentral.TLSSkipVerify = skip
		upstream := NewUpstream(cfg, nil)
		for _, policy := range []*tls.Config{upstream.http.Transport.(*http.Transport).TLSClientConfig, upstream.meshDialer().TLSClientConfig} {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			clientRaw, serverRaw := net.Pipe()
			done := make(chan error, 1)
			go func() { done <- tls.Server(serverRaw, serverConfig).HandshakeContext(ctx) }()
			clientPolicy := policy.Clone()
			clientPolicy.ServerName = "container.internal"
			err := tls.Client(clientRaw, clientPolicy).HandshakeContext(ctx)
			clientRaw.Close()
			serverRaw.Close()
			cancel()
			<-done
			if skip && err != nil {
				t.Fatalf("explicit opt-out rejected internal self-signed certificate: %v", err)
			}
			if !skip && err == nil {
				t.Fatal("default policy accepted untrusted, mismatched certificate")
			}
		}
	}
}
