package edge

import (
	"errors"
	"testing"
)

func TestSelectSSHEndpoint(t *testing.T) {
	tests := []struct {
		name            string
		addresses       []string
		listeners, want string
	}{
		{"custom port", []string{"0.0.0.0:2222", "[::]:2222"}, "LISTEN 0 128 0.0.0.0:2222 0.0.0.0:*", "127.0.0.1:2222"},
		{"ipv6 only", []string{"[::]:2200"}, "LISTEN 0 128 [::]:2200 [::]:*", "[::1]:2200"},
		{"actual listener", []string{"0.0.0.0:22", "0.0.0.0:2022"}, "LISTEN 0 128 0.0.0.0:2022 0.0.0.0:*", "127.0.0.1:2022"},
		{"wildcard socket", []string{"[::]:2222"}, "LISTEN 0 128 *:2222 *:*", "[::1]:2222"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectSSHEndpoint(tt.addresses, tt.listeners)
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	if _, err := selectSSHEndpoint([]string{"192.168.1.1:22"}, "LISTEN 0 128 192.168.1.1:22 *:*"); err == nil {
		t.Fatal("must not connect non-loopback addresses")
	}
}

func TestSocketActivationOverridesSSHDConfiguration(t *testing.T) {
	endpoint, err := discoverSSHEndpoint(func(name string, args ...string) (string, error) {
		switch name {
		case "systemctl":
			return "Listen=[::]:2244 (Stream)\nActiveState=active\n", nil
		case "ss":
			return "LISTEN 0 128 *:2244 *:*", nil
		default:
			t.Fatal("active socket must override sshd configuration")
			return "", errors.New("unexpected command")
		}
	})
	if err != nil || endpoint != "[::1]:2244" {
		t.Fatalf("%q %v", endpoint, err)
	}
}

func TestDiscoveryUsesEffectiveConfigAndDoesNotFallbackTo22(t *testing.T) {
	endpoint, err := discoverSSHEndpoint(func(name string, args ...string) (string, error) {
		switch name {
		case "systemctl":
			return "ActiveState=inactive", nil
		case "/usr/sbin/sshd":
			if len(args) != 1 || args[0] != "-G" {
				t.Fatal("must dump effective config without loading private keys")
			}
			return "port 2222\nlistenaddress 0.0.0.0:2222", nil
		case "ss":
			return "LISTEN 0 128 0.0.0.0:2222 *:*", nil
		}
		return "", errors.New("unknown command")
	})
	if err != nil || endpoint != "127.0.0.1:2222" {
		t.Fatalf("%q %v", endpoint, err)
	}
}
