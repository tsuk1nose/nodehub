package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/url"
	"nodehub/internal/remote"
	"os"
	"time"
)

type Config struct {
	Terminal    remote.Config `json:"terminal"`
	Listen      string        `json:"listen"`
	Database    string        `json:"database"`
	WebDir      string        `json:"web_dir"`
	PollSeconds int           `json:"poll_interval_seconds"`
	Auth        struct {
		Enabled      bool   `json:"enabled"`
		Username     string `json:"username"`
		PasswordHash string `json:"password_hash"`
		SecureCookie bool   `json:"secure_cookie"`
		SessionHours int    `json:"session_hours"`
	} `json:"auth"`
	Headscale struct {
		Enabled     bool     `json:"enabled"`
		InternalURL string   `json:"internal_url"`
		PublicURL   string   `json:"public_url"`
		APIKey      string   `json:"api_key"`
		UserID      string   `json:"user_id"`
		Tags        []string `json:"tags"`
	} `json:"headscale"`
	MeshCentral struct {
		Enabled           bool   `json:"enabled"`
		InternalURL       string `json:"internal_url"`
		PublicURL         string `json:"public_url"`
		Username          string `json:"username"`
		Password          string `json:"password"`
		MeshID            string `json:"mesh_id"`
		DeviceURLTemplate string `json:"device_url_template"`
	} `json:"meshcentral"`
}

func LoadConfig(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.WebDir == "" {
		c.WebDir = "./web/dist"
	}
	if c.Database == "" {
		return c, errors.New("database is required")
	}
	if c.PollSeconds == 0 {
		c.PollSeconds = 60
	}
	if c.PollSeconds < 5 {
		return c, errors.New("poll interval must be at least 5 seconds")
	}
	if c.Auth.SessionHours == 0 {
		c.Auth.SessionHours = 12
	}
	if c.Auth.SessionHours < 1 || c.Auth.SessionHours > 168 {
		return c, errors.New("session_hours must be 1..168")
	}
	if c.Auth.Enabled {
		if c.Auth.Username == "" {
			return c, errors.New("auth username required")
		}
		if _, e = bcrypt.Cost([]byte(c.Auth.PasswordHash)); e != nil {
			return c, errors.New("configure a valid bcrypt password hash")
		}
	}
	for _, v := range []struct {
		enabled bool
		raw     string
		ws      bool
	}{{c.Headscale.Enabled, c.Headscale.InternalURL, false}, {c.Headscale.Enabled, c.Headscale.PublicURL, false}, {c.MeshCentral.Enabled, c.MeshCentral.InternalURL, true}, {c.MeshCentral.Enabled, c.MeshCentral.PublicURL, false}} {
		if !v.enabled {
			continue
		}
		u, e := url.Parse(v.raw)
		if e != nil || u.Host == "" || u.User != nil {
			return c, fmt.Errorf("invalid upstream URL")
		}
		if v.ws {
			if u.Scheme != "ws" && u.Scheme != "wss" {
				return c, errors.New("MeshCentral internal URL must be ws(s)")
			}
		} else if u.Scheme != "http" && u.Scheme != "https" {
			return c, errors.New("URL must be http(s)")
		}
	}
	if c.Headscale.Enabled && (c.Headscale.APIKey == "" || c.Headscale.UserID == "") {
		return c, errors.New("Headscale API key and user_id required")
	}
	if c.MeshCentral.Enabled && (c.MeshCentral.Username == "" || c.MeshCentral.Password == "" || c.MeshCentral.MeshID == "") {
		return c, errors.New("MeshCentral username, password and mesh_id required")
	}
	c.Terminal.Defaults()
	if e = c.Terminal.Validate(); e != nil {
		return c, e
	}
	return c, nil
}
func (c Config) PollInterval() time.Duration { return time.Duration(c.PollSeconds) * time.Second }
