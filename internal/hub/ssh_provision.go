package hub

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"
	"nodehub/internal/remote"
)

const sshLoginUser = "root"

func parseHostKey(raw string) (ssh.PublicKey, string, error) {
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(raw))
	if err != nil || len(options) != 0 || len(strings.TrimSpace(string(rest))) != 0 || key.Type() != ssh.KeyAlgoED25519 {
		return nil, "", errors.New("one Ed25519 host key required")
	}
	return key, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))), nil
}

// Never replace established host keys or login keys on a bootstrap retry.
func (s *Store) provisionSSH(serial, rawHost string) (string, error) {
	_, host, err := parseHostKey(rawHost)
	if err != nil {
		return "", err
	}
	identity, err := s.identity(serial)
	if err != nil || identity.Revoked {
		return "", ErrUnauthorized
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(key, "nodehub per-device login")
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO terminal_ssh(serial,username,private_key,host_key) VALUES(?,?,?,?) ON CONFLICT(serial) DO NOTHING`, serial, sshLoginUser, pem.EncodeToMemory(block), host)
	if err != nil {
		return "", err
	}
	var savedHost string
	var private []byte
	err = s.db.QueryRow(`SELECT host_key,private_key FROM terminal_ssh WHERE serial=?`, serial).Scan(&savedHost, &private)
	if err != nil {
		return "", err
	}
	if savedHost != host {
		return "", ErrConflict
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func (a *Server) sshTarget(serial string) (remote.Target, error) {
	var user, host string
	var private []byte
	err := a.s.db.QueryRow(`SELECT username,private_key,host_key FROM terminal_ssh WHERE serial=? AND ready=1`, serial).Scan(&user, &private, &host)
	if errors.Is(err, sql.ErrNoRows) {
		if target, ok := a.targets[serial]; ok {
			return target, nil
		}
	}
	if err != nil {
		return remote.Target{}, err
	}
	signer, err := ssh.ParsePrivateKey(private)
	if err != nil {
		return remote.Target{}, err
	}
	key, _, err := parseHostKey(host)
	if err != nil {
		return remote.Target{}, err
	}
	return remote.Target{User: user, Signer: signer, HostKey: key}, nil
}

func (a *Server) sshProvisionRoutes() {
	for _, confirm := range []bool{false, true} {
		path := "POST /api/node/{serial}/ssh-provision"
		if confirm {
			path += "/confirm"
		}
		a.mux.HandleFunc(path, a.node(func(w http.ResponseWriter, r *http.Request) {
			if !a.cfg.Terminal.Enabled {
				fail(w, 503, "Native terminal is disabled")
				return
			}
			var body struct {
				HostKey  string `json:"host_key"`
				Username string `json:"username"`
			}
			if !decode(w, r, &body) {
				return
			}
			if body.Username != sshLoginUser {
				fail(w, 400, "Automatic SSH provisioning requires root login; update the initializer")
				return
			}
			serial := r.PathValue("serial")
			if confirm {
				var exists int
				if err := a.s.db.QueryRow(`SELECT 1 FROM terminal_ssh WHERE serial=?`, serial).Scan(&exists); err != nil {
					fail(w, 409, "Request SSH provisioning before confirming installation")
					return
				}
			}
			public, err := a.s.provisionSSH(serial, body.HostKey)
			if err != nil {
				fail(w, 409, "SSH provisioning denied; check the Agent identity and pinned host key; replacement is not allowed")
				return
			}
			if confirm {
				if _, err = a.s.db.Exec(`UPDATE terminal_ssh SET ready=1,username=? WHERE serial=?`, sshLoginUser, serial); err != nil {
					fail(w, 500, "Cannot confirm SSH provisioning")
					return
				}
			}
			action := "ssh_provisioned"
			if confirm {
				action = "ssh_confirmed"
			}
			if err = a.s.agentEvent(serial, action, r.RemoteAddr); err != nil {
				fail(w, 500, "Cannot save the SSH audit; retry the request")
				return
			}
			reply(w, 200, map[string]any{"username": sshLoginUser, "authorized_key": public, "confirmed": confirm})
		}))
	}
}
