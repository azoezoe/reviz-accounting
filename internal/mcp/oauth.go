// Package mcp implements the remote MCP authorization endpoints. The app's
// normal login session is only used to approve an OAuth PKCE request; it is
// never handed to an MCP client.
package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hcchien/reviz-accounting/internal/auth"
)

type Server struct{ DB *sql.DB }

func randomValue(n int) (string, error) {
	b := make([]byte, n)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}
func hash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }

func (s *Server) Metadata(w http.ResponseWriter, r *http.Request) {
	base := "https://" + r.Host
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/oauth/authorize", "token_endpoint": base + "/oauth/token", "registration_endpoint": base + "/oauth/register", "code_challenge_methods_supported": []string{"S256"}, "grant_types_supported": []string{"authorization_code"}, "response_types_supported": []string{"code"}})
}

func (s *Server) ProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := "https://" + r.Host
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}})
}

func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || len(in.RedirectURIs) == 0 {
		http.Error(w, "redirect_uris required", 400)
		return
	}
	for _, raw := range in.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			http.Error(w, "invalid redirect_uri", 400)
			return
		}
	}
	id, err := randomValue(24)
	if err != nil {
		http.Error(w, "random failure", 500)
		return
	}
	b, _ := json.Marshal(in.RedirectURIs)
	if _, err = s.DB.Exec(`INSERT INTO mcp_oauth_clients(id,redirect_uris,client_name) VALUES(?,?,?)`, id, string(b), strings.TrimSpace(in.ClientName)); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"client_id": id, "redirect_uris": in.RedirectURIs, "client_name": in.ClientName, "token_endpoint_auth_method": "none"})
}

func (s *Server) Authorize(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") == "" || q.Get("redirect_uri") == "" || q.Get("code_challenge") == "" {
		http.Error(w, "invalid OAuth authorization request", 400)
		return
	}
	var redirectURIs, name string
	if err := s.DB.QueryRow(`SELECT redirect_uris,client_name FROM mcp_oauth_clients WHERE id=?`, q.Get("client_id")).Scan(&redirectURIs, &name); err != nil || !containsURI(redirectURIs, q.Get("redirect_uri")) {
		http.Error(w, "unregistered redirect URI", 400)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// All values are URL encoded before being placed back in the form action.
	_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><title>授權 ReViz MCP</title><main style='max-width:600px;margin:5rem auto;font-family:sans-serif'><h1>授權 ReViz MCP</h1><p>「" + htmlText(name) + "」要求以 <b>" + htmlText(u.Username) + "</b> 身分讀取及依權限修改帳務。</p><form method=post action='/oauth/authorize'><input type=hidden name=request value='" + url.QueryEscape(r.URL.RawQuery) + "'><button type=submit>同意並連線</button></form></main>"))
}

func (s *Server) Approve(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	if u == nil {
		http.Error(w, "login required", 401)
		return
	}
	_ = r.ParseForm()
	q, err := url.ParseQuery(r.FormValue("request"))
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	clientID, redirect, challenge, state := q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("state")
	var uris string
	if err := s.DB.QueryRow(`SELECT redirect_uris FROM mcp_oauth_clients WHERE id=?`, clientID).Scan(&uris); err != nil || !containsURI(uris, redirect) || challenge == "" {
		http.Error(w, "invalid client", 400)
		return
	}
	code, err := randomValue(32)
	if err != nil {
		http.Error(w, "random failure", 500)
		return
	}
	_, err = s.DB.Exec(`INSERT INTO mcp_authorization_codes(code_hash,client_id,user_id,redirect_uri,code_challenge,expires_at) VALUES(?,?,?,?,?,?)`, hash(code), clientID, u.ID, redirect, challenge, time.Now().Add(5*time.Minute).UTC().Format(time.RFC3339))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	dest, _ := url.Parse(redirect)
	v := dest.Query()
	v.Set("code", code)
	if state != "" {
		v.Set("state", state)
	}
	dest.RawQuery = v.Encode()
	http.Redirect(w, r, dest.String(), http.StatusSeeOther)
}

func containsURI(raw, target string) bool {
	var xs []string
	return json.Unmarshal([]byte(raw), &xs) == nil && func() bool {
		for _, x := range xs {
			if x == target {
				return true
			}
		}
		return false
	}()
}
func htmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "'", "&#39;", "\"", "&#34;").Replace(s)
}

func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.FormValue("grant_type") != "authorization_code" {
		http.Error(w, "unsupported grant", 400)
		return
	}
	code, verifier := r.FormValue("code"), r.FormValue("code_verifier")
	if code == "" || verifier == "" {
		http.Error(w, "code and verifier required", 400)
		return
	}
	var clientID string
	var userID int64
	var redirect, challenge, expires string
	var used sql.NullString
	err := s.DB.QueryRow(`SELECT client_id,user_id,redirect_uri,code_challenge,expires_at,used_at FROM mcp_authorization_codes WHERE code_hash=?`, hash(code)).Scan(&clientID, &userID, &redirect, &challenge, &expires, &used)
	if err != nil || used.Valid || r.FormValue("client_id") != clientID || r.FormValue("redirect_uri") != redirect || expired(expires) || pkce(verifier) != challenge {
		http.Error(w, "invalid authorization code", 400)
		return
	}
	_, _ = s.DB.Exec(`UPDATE mcp_authorization_codes SET used_at=CURRENT_TIMESTAMP::text WHERE code_hash=?`, hash(code))
	token, err := randomValue(32)
	if err != nil {
		http.Error(w, "random failure", 500)
		return
	}
	exp := time.Now().Add(8 * time.Hour).UTC().Format(time.RFC3339)
	_, err = s.DB.Exec(`INSERT INTO mcp_access_tokens(token_hash,client_id,user_id,expires_at) VALUES(?,?,?,?)`, hash(token), clientID, userID, exp)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 28800})
}
func pkce(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func expired(s string) bool {
	t, e := time.Parse(time.RFC3339, s)
	return e != nil || time.Now().After(t)
}

var _ = errors.New
