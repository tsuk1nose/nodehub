package remote

import (
	"errors"
	"os"
	"testing"
)

func TestSSHErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		err  error
		host bool
		want string
	}{
		{errors.New("ssh: handshake failed: host key mismatch"), true, "ssh_host_key_mismatch"},
		{errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none publickey]"), false, "ssh_authentication_failed"},
		{os.ErrDeadlineExceeded, false, "ssh_handshake_timeout"},
		{errors.New("EOF"), false, "ssh_handshake_failed"},
	} {
		code, message := classifySSHError(tt.err, tt.host)
		if code != tt.want || message == "" {
			t.Fatalf("got %q %q; want %q", code, message, tt.want)
		}
	}
}
