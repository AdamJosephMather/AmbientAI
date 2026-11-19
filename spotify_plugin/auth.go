package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const scopes = "user-read-playback-state user-read-currently-playing user-modify-playback-state"

type token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	ClientID     string    `json:"client_id"`
}

type auth struct {
	mu       sync.Mutex
	cfg      config
	http     *http.Client
	tokenURL string
	token    token
}

func newAuth(cfg config) (*auth, error) {
	a := &auth{cfg: cfg, http: &http.Client{Timeout: time.Duration(cfg.RequestTimeoutSeconds) * time.Second}, tokenURL: "https://accounts.spotify.com/api/token"}
	data, err := os.ReadFile(cfg.TokenFile)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil || json.Unmarshal(data, &a.token) != nil {
		return nil, fmt.Errorf("cannot read token cache; remove it and run --authorize to recreate it")
	}
	if a.token.ClientID != cfg.ClientID {
		return nil, fmt.Errorf("token cache belongs to another client_id; remove it and run --authorize")
	}
	return a, nil
}

func (a *auth) saveToken(t token) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	// Same-directory temporary file plus rename prevents partial caches. os.CreateTemp
	// uses mode 0600 on Unix; Windows permissions follow the directory's ACL.
	f, err := os.CreateTemp(filepath.Dir(a.cfg.TokenFile), ".spotify-tokens-*")
	if err != nil {
		return fmt.Errorf("cannot create token cache")
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("cannot write token cache")
	}
	if err := os.Rename(f.Name(), a.cfg.TokenFile); err != nil {
		return fmt.Errorf("cannot replace token cache")
	}
	return nil
}

// The caller holds mu. Refresh tokens may rotate; persist the replacement before
// exposing it to other requests. No token or raw OAuth response enters diagnostics.
func (a *auth) exchange(ctx context.Context, values url.Values) error {
	values.Set("client_id", a.cfg.ClientID)
	req, err := http.NewRequestWithContext(ctx, "POST", a.tokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("Spotify authorization request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("Spotify authorization failed (HTTP %d); check your app settings or run --authorize again", res.StatusCode)
	}
	var body struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body) != nil || body.AccessToken == "" || body.ExpiresIn <= 0 {
		return fmt.Errorf("invalid Spotify token response")
	}
	t := token{AccessToken: body.AccessToken, RefreshToken: body.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(body.ExpiresIn) * time.Second), ClientID: a.cfg.ClientID}
	if t.RefreshToken == "" {
		t.RefreshToken = a.token.RefreshToken
	}
	if t.RefreshToken == "" {
		return fmt.Errorf("Spotify did not return a refresh token; authorize again")
	}
	a.token = t // Retain rotated credentials in memory even if disk persistence fails.
	return a.saveToken(t)
}

func (a *auth) accessToken(ctx context.Context, rejected string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token.AccessToken != "" && time.Until(a.token.ExpiresAt) > 30*time.Second && (rejected == "" || rejected != a.token.AccessToken) {
		return a.token.AccessToken, nil
	}
	if a.token.RefreshToken == "" {
		return "", fmt.Errorf("Spotify is not authorized: run this plugin manually with --authorize first")
	}
	if err := a.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {a.token.RefreshToken}}); err != nil {
		return "", err
	}
	return a.token.AccessToken, nil
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (a *auth) authorize(ctx context.Context) error {
	return a.authorizeWithBrowser(ctx, openBrowser)
}

func (a *auth) authorizeWithBrowser(ctx context.Context, browse func(string) error) error {
	state, err := randomString()
	if err != nil {
		return err
	}
	verifier, err := randomString()
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(verifier))
	u, _ := url.Parse(a.cfg.RedirectURI) // Validated by loadConfig.
	listener, err := net.Listen("tcp", u.Host)
	if err != nil {
		return fmt.Errorf("cannot listen on OAuth callback address: %w", err)
	}
	defer listener.Close()
	type callback struct {
		code string
		err  error
	}
	result := make(chan callback, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != u.Path || r.Method != http.MethodGet || r.URL.Query().Get("state") != state {
			http.Error(w, "Invalid authorization callback", 400)
			return
		}
		once.Do(func() {
			code := r.URL.Query().Get("code")
			if r.URL.Query().Get("error") != "" || code == "" {
				result <- callback{err: fmt.Errorf("Spotify authorization was denied or returned no code")}
				return
			}
			result <- callback{code: code}
		})
		fmt.Fprintln(w, "Authorization received. You can close this tab and return to the terminal.")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	values := url.Values{"response_type": {"code"}, "client_id": {a.cfg.ClientID}, "redirect_uri": {a.cfg.RedirectURI}, "scope": {scopes}, "state": {state}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}}
	address := "https://accounts.spotify.com/authorize?" + values.Encode()
	fmt.Fprintln(os.Stderr, "Authorize your own Spotify developer app in the browser:\n"+address)
	if err := browse(address); err != nil {
		fmt.Fprintln(os.Stderr, "Browser did not open; use the URL above.")
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("Spotify authorization timed out or was cancelled")
	case response := <-result:
		if response.err != nil {
			return response.err
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.exchange(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {response.code}, "redirect_uri": {a.cfg.RedirectURI}, "code_verifier": {verifier}})
	}
}
