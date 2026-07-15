package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	sessionCookie = "tsundere_session"
	stateCookie   = "tsundere_oauth_state"
	sessionTTL    = 7 * 24 * time.Hour
)

// ---- stateless HMAC-signed session cookie: "login|expiry|signature" ----

func (s *Server) signSession(login string, exp int64) string {
	payload := login + "|" + strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, s.sessionSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) verifySession(value string) (string, bool) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, s.sessionSecret)
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return "", false
	}
	fields := strings.SplitN(string(raw), "|", 2)
	if len(fields) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return fields[0], true
}

// currentUser returns the logged-in GitHub login, or "" if not authenticated.
func (s *Server) currentUser(r *http.Request) string {
	if s.cfg.DevNoAuth {
		return "dev"
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	login, ok := s.verifySession(c.Value)
	if !ok || !s.cfg.IsAdmin(login) {
		return ""
	}
	return login
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.currentUser(r) == "" {
			writeErr(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next(w, r)
	}
}

func (s *Server) secureCookies() bool {
	return strings.HasPrefix(s.cfg.BaseURL, "https://")
}

// ---- GitHub OAuth flow ----

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.GitHubClientID == "" {
		http.Error(w, "GitHub OAuth is not configured (set TSUNDERE_GITHUB_CLIENT_ID / TSUNDERE_GITHUB_CLIENT_SECRET)", http.StatusServiceUnavailable)
		return
	}
	buf := make([]byte, 16)
	rand.Read(buf)
	state := hex.EncodeToString(buf)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: state, Path: "/", MaxAge: 600,
		HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{
		"client_id":    {s.cfg.GitHubClientID},
		"redirect_uri": {s.cfg.BaseURL + "/auth/callback"},
		"state":        {state},
		"scope":        {"read:user"},
	}
	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+q.Encode(), http.StatusFound)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	stateC, err := r.Cookie(stateCookie)
	if err != nil || stateC.Value == "" || r.URL.Query().Get("state") != stateC.Value {
		http.Error(w, "OAuth state mismatch. Try logging in again.", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	// Exchange code for an access token.
	form := url.Values{
		"client_id":     {s.cfg.GitHubClientID},
		"client_secret": {s.cfg.GitHubClientSecret},
		"code":          {code},
		"redirect_uri":  {s.cfg.BaseURL + "/auth/callback"},
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil || tok.AccessToken == "" {
		http.Error(w, "token exchange failed: "+tok.Error, http.StatusBadGateway)
		return
	}

	// Fetch the GitHub login of whoever just authorized.
	uReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/user", nil)
	uReq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uReq.Header.Set("Accept", "application/vnd.github+json")
	uResp, err := http.DefaultClient.Do(uReq)
	if err != nil {
		http.Error(w, "fetching GitHub user failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer uResp.Body.Close()
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(uResp.Body).Decode(&user); err != nil || user.Login == "" {
		http.Error(w, "fetching GitHub user failed", http.StatusBadGateway)
		return
	}

	if !s.cfg.IsAdmin(user.Login) {
		http.Error(w, fmt.Sprintf("Hmph. '%s' is not on the admin list. It's not like I wanted to let you in anyway.", user.Login), http.StatusForbidden)
		return
	}

	exp := time.Now().Add(sessionTTL).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.signSession(user.Login, exp), Path: "/",
		Expires: time.Unix(exp, 0), HttpOnly: true, Secure: s.secureCookies(), SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/admin", http.StatusFound)
}
