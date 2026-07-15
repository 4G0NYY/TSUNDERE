package server

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
)

//go:embed webfs
var webFS embed.FS

type Server struct {
	cfg           Config
	store         *Store
	engine        *Engine
	notifier      *Notifier
	sessionSecret []byte
}

func New(cfg Config) (*Server, error) {
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	secret, err := store.SessionSecret()
	if err != nil {
		return nil, fmt.Errorf("session secret: %w", err)
	}
	notifier := NewNotifier(store)
	return &Server{
		cfg:           cfg,
		store:         store,
		engine:        NewEngine(store, notifier),
		notifier:      notifier,
		sessionSecret: secret,
	}, nil
}

func (s *Server) Run() error {
	s.engine.Run()

	mux := http.NewServeMux()

	// Static assets (css/js) live under /assets/.
	sub, err := fs.Sub(webFS, "webfs")
	if err != nil {
		return err
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServerFS(sub)))

	servePage := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, err := webFS.ReadFile("webfs/" + name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(b)
		}
	}
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin", http.StatusFound)
	})
	mux.HandleFunc("GET /admin", servePage("admin.html"))
	mux.HandleFunc("GET /status/{slug}", servePage("status.html"))

	// OAuth
	mux.HandleFunc("GET /auth/login", s.handleLogin)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("GET /auth/logout", s.handleLogout)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)

	// Agent API (Bearer token auth)
	mux.HandleFunc("GET /api/agent/config", s.handleAgentConfig)
	mux.HandleFunc("POST /api/agent/results", s.handleAgentResults)
	mux.HandleFunc("POST /api/agent/inventory", s.handleAgentInventory)

	// Public status page API
	mux.HandleFunc("GET /api/status/{slug}", s.handlePublicStatus)

	// Admin API (session auth)
	admin := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, s.requireAdmin(h))
	}
	mux.HandleFunc("GET /api/admin/me", s.handleMe) // returns 401 when logged out; used by the login screen
	admin("GET /api/admin/overview", s.handleOverview)

	admin("GET /api/admin/agents", s.handleListAgents)
	admin("POST /api/admin/agents", s.handleCreateAgent)
	admin("PUT /api/admin/agents/{id}", s.handleRenameAgent)
	admin("POST /api/admin/agents/{id}/regenerate-token", s.handleRegenerateAgentToken)
	admin("DELETE /api/admin/agents/{id}", s.handleDeleteAgent)

	admin("GET /api/admin/monitors", s.handleListMonitors)
	admin("POST /api/admin/monitors", s.handleCreateMonitor)
	admin("PUT /api/admin/monitors/{id}", s.handleUpdateMonitor)
	admin("DELETE /api/admin/monitors/{id}", s.handleDeleteMonitor)
	admin("GET /api/admin/monitors/{id}/heartbeats", s.handleMonitorHeartbeats)

	admin("GET /api/admin/status-pages", s.handleListStatusPages)
	admin("POST /api/admin/status-pages", s.handleCreateStatusPage)
	admin("PUT /api/admin/status-pages/{id}", s.handleUpdateStatusPage)
	admin("DELETE /api/admin/status-pages/{id}", s.handleDeleteStatusPage)

	admin("GET /api/admin/incidents", s.handleListIncidents)
	admin("POST /api/admin/incidents", s.handleCreateIncident)
	admin("PUT /api/admin/incidents/{id}", s.handleUpdateIncident)
	admin("POST /api/admin/incidents/{id}/resolve", s.handleResolveIncident)
	admin("DELETE /api/admin/incidents/{id}", s.handleDeleteIncident)

	admin("GET /api/admin/maintenances", s.handleListMaintenances)
	admin("POST /api/admin/maintenances", s.handleCreateMaintenance)
	admin("PUT /api/admin/maintenances/{id}", s.handleUpdateMaintenance)
	admin("DELETE /api/admin/maintenances/{id}", s.handleDeleteMaintenance)

	admin("GET /api/admin/settings", s.handleGetSettings)
	admin("PUT /api/admin/settings", s.handlePutSettings)
	admin("POST /api/admin/settings/test-discord", s.handleTestDiscord)
	admin("POST /api/admin/settings/test-email", s.handleTestEmail)

	if s.cfg.DevNoAuth {
		log.Printf("WARNING: TSUNDERE_DEV_NO_AUTH is set — admin auth is DISABLED. Never do this in production, baka.")
	}
	log.Printf("TSUNDERE server listening on %s (base URL %s)", s.cfg.Listen, s.cfg.BaseURL)
	return http.ListenAndServe(s.cfg.Listen, mux)
}
