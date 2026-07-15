package server

import (
	"os"
	"strings"
)

// Config holds everything that must be known before the web UI is reachable.
// All of this comes from the environment; everything else (alerting, site
// title, ...) lives in the settings table and is editable from the admin UI.
type Config struct {
	Listen             string // e.g. ":8420"
	DBPath             string // sqlite file path
	BaseURL            string // public URL of the server, no trailing slash (needed for the OAuth callback)
	GitHubClientID     string
	GitHubClientSecret string
	AdminUsers         []string // GitHub logins allowed into the admin UI
	DevNoAuth          bool     // TSUNDERE_DEV_NO_AUTH=1 skips OAuth entirely. Local development only.
}

func LoadConfig() Config {
	cfg := Config{
		Listen:             envOr("TSUNDERE_LISTEN", ":8420"),
		DBPath:             envOr("TSUNDERE_DB", "tsundere.db"),
		BaseURL:            strings.TrimRight(envOr("TSUNDERE_BASE_URL", "http://localhost:8420"), "/"),
		GitHubClientID:     os.Getenv("TSUNDERE_GITHUB_CLIENT_ID"),
		GitHubClientSecret: os.Getenv("TSUNDERE_GITHUB_CLIENT_SECRET"),
		DevNoAuth:          os.Getenv("TSUNDERE_DEV_NO_AUTH") == "1",
	}
	for _, u := range strings.Split(os.Getenv("TSUNDERE_ADMIN_USERS"), ",") {
		u = strings.TrimSpace(u)
		if u != "" {
			cfg.AdminUsers = append(cfg.AdminUsers, u)
		}
	}
	return cfg
}

func (c Config) IsAdmin(login string) bool {
	for _, u := range c.AdminUsers {
		if strings.EqualFold(u, login) {
			return true
		}
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
