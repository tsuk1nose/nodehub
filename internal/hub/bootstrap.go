package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var bootstrapCommon = []string{
	"client/bootstrap.py", "client/nodehub-init.py", "client/mesh-install.py",
	"client/install.sh", "client/nodehub-init.service", "client/enable-overlayroot.example.sh",
	"agent/install.sh", "agent/config.example.json", "agent/nodehub-agent.service",
}

func (a *Server) bootstrapFile(name string) (*os.File, error) {
	path := filepath.Join(a.cfg.Bootstrap.AssetsDir, filepath.FromSlash(name))
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, os.ErrPermission
	}
	return os.Open(path)
}
func (a *Server) bootstrapAvailable() bool {
	if !a.cfg.Bootstrap.Enabled {
		return false
	}
	names := append([]string{"client/bootstrap.sh"}, bootstrapCommon...)
	if a.cfg.Terminal.Enabled {
		names = append(names, "bin/nodehub-agent-linux-amd64", "bin/nodehub-agent-linux-arm64")
	}
	for _, name := range names {
		f, err := a.bootstrapFile(name)
		if err != nil {
			return false
		}
		f.Close()
	}
	return true
}
func (a *Server) bootstrapRoutes() {
	a.mux.HandleFunc("GET /install.sh", func(w http.ResponseWriter, r *http.Request) {
		if !a.bootstrapAvailable() {
			http.Error(w, "Bootstrap assets are unavailable", 503)
			return
		}
		f, err := a.bootstrapFile("client/bootstrap.sh")
		if err != nil {
			http.Error(w, "Bootstrap assets are unavailable", 503)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.Error(w, "Bootstrap assets are unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "install.sh", st.ModTime(), f)
	})
	a.mux.HandleFunc("GET /bootstrap/manifest", func(w http.ResponseWriter, r *http.Request) {
		if !a.bootstrapAvailable() {
			fail(w, 503, "Bootstrap assets are unavailable")
			return
		}
		arch := r.URL.Query().Get("arch")
		if arch != "amd64" && arch != "arm64" {
			fail(w, 400, "Unsupported bootstrap architecture")
			return
		}
		names := append([]string{}, bootstrapCommon...)
		if a.cfg.Terminal.Enabled {
			names = append(names, "bin/nodehub-agent-linux-"+arch)
		}
		files := map[string]any{}
		for _, name := range names {
			f, err := a.bootstrapFile(name)
			if err != nil {
				fail(w, 503, "Bootstrap package is incomplete")
				return
			}
			hash := sha256.New()
			size, err := io.Copy(hash, f)
			f.Close()
			if err != nil {
				fail(w, 503, "Cannot read bootstrap assets")
				return
			}
			files[name] = map[string]any{"sha256": hex.EncodeToString(hash.Sum(nil)), "size": size}
		}
		w.Header().Set("Cache-Control", "no-store")
		reply(w, 200, map[string]any{"files": files, "terminal_enabled": a.cfg.Terminal.Enabled, "heartbeat_seconds": a.cfg.Terminal.HeartbeatSeconds})
	})
	a.mux.HandleFunc("GET /bootstrap/files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		if !a.bootstrapAvailable() {
			fail(w, 503, "Bootstrap assets are unavailable")
			return
		}
		name := r.PathValue("path")
		allowed := name == "bin/nodehub-agent-linux-amd64" || name == "bin/nodehub-agent-linux-arm64"
		for _, known := range bootstrapCommon {
			allowed = allowed || name == known
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
		f, err := a.bootstrapFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, filepath.Base(name), st.ModTime(), f)
	})
	a.mux.HandleFunc("GET /api/node/{serial}/bootstrap", a.node(func(w http.ResponseWriter, r *http.Request) {
		if !a.cfg.Bootstrap.Enabled || !a.cfg.MeshCentral.Enabled {
			fail(w, 503, "Automatic MeshAgent setup is unavailable")
			return
		}
		if r.URL.Query().Get("arch") != "amd64" {
			fail(w, 400, "Automatic MeshAgent download currently supports amd64; supply a reviewed hook for other architectures")
			return
		}
		base, err := url.Parse(a.cfg.MeshCentral.PublicURL)
		if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
			fail(w, 503, "MeshCentral public download URL must use HTTPS")
			return
		}
		parts := strings.Split(a.cfg.MeshCentral.MeshID, "/")
		if len(parts) != 3 || parts[0] != "mesh" || parts[2] == "" {
			fail(w, 503, "A full MeshCentral group ID is required")
			return
		}
		root := strings.TrimRight(base.String(), "/")
		reply(w, 200, map[string]string{"binary_url": root + "/meshagents?id=6", "settings_url": root + "/meshsettings?" + url.Values{"id": {parts[2]}}.Encode()})
	}))
}
