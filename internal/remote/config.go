package remote

import (
	"errors"
	"golang.org/x/crypto/ssh"
	"net/url"
	"os"
	"strings"
	"time"
)

type SSHConfig struct {
	Username       string `json:"username"`
	PrivateKeyFile string `json:"private_key_file"`
	HostKey        string `json:"host_key"`
}
type Config struct {
	Enabled          bool                 `json:"enabled"`
	AllowedOrigins   []string             `json:"allowed_origins"`
	HeartbeatSeconds int                  `json:"heartbeat_seconds"`
	TimeoutSeconds   int                  `json:"timeout_seconds"`
	MaxSessions      int                  `json:"max_sessions"`
	MaxPerNode       int                  `json:"max_sessions_per_node"`
	IdleSeconds      int                  `json:"idle_seconds"`
	MaxSeconds       int                  `json:"max_duration_seconds"`
	Nodes            map[string]SSHConfig `json:"nodes"`
}
type Target struct {
	User    string
	Signer  ssh.Signer
	HostKey ssh.PublicKey
}

func (c *Config) Defaults() {
	if c.HeartbeatSeconds == 0 {
		c.HeartbeatSeconds = 30
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 90
	}
	if c.MaxSessions == 0 {
		c.MaxSessions = 10
	}
	if c.MaxPerNode == 0 {
		c.MaxPerNode = 2
	}
	if c.IdleSeconds == 0 {
		c.IdleSeconds = 300
	}
	if c.MaxSeconds == 0 {
		c.MaxSeconds = 3600
	}
}
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.AllowedOrigins) == 0 {
		return errors.New("terminal.allowed_origins required")
	}
	for _, v := range c.AllowedOrigins {
		u, e := url.Parse(v)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("invalid terminal origin")
		}
	}
	if c.HeartbeatSeconds < 1 || c.TimeoutSeconds <= c.HeartbeatSeconds || c.MaxSessions < 1 || c.MaxPerNode < 1 || c.IdleSeconds < 1 || c.MaxSeconds < 1 {
		return errors.New("invalid terminal limits")
	}
	return nil
}
func (c Config) Targets() (map[string]Target, error) {
	out := map[string]Target{}
	if !c.Enabled {
		return out, nil
	}
	for serial, v := range c.Nodes {
		if len(serial) != 12 || strings.Trim(serial, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" || v.Username == "" {
			return nil, errors.New("invalid terminal node or SSH user")
		}
		st, e := os.Stat(v.PrivateKeyFile)
		if e != nil {
			return nil, errors.New("cannot read terminal SSH key file")
		}
		if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
			return nil, errors.New("SSH private key must be a regular file with mode 0600 or stricter")
		}
		raw, e := os.ReadFile(v.PrivateKeyFile)
		if e != nil {
			return nil, e
		}
		signer, e := ssh.ParsePrivateKey(raw)
		if e != nil {
			return nil, errors.New("invalid SSH private key; encrypted keys not supported in this MVP")
		}
		key, _, _, rest, e := ssh.ParseAuthorizedKey([]byte(v.HostKey))
		if e != nil || len(strings.TrimSpace(string(rest))) != 0 {
			return nil, errors.New("one pinned SSH host public key required")
		}
		out[serial] = Target{v.Username, signer, key}
	}
	return out, nil
}
func (c Config) Timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }
