package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPKCEAuthorizationValidatesStateAndPersistsTokens(t *testing.T) {
	// Reserve an ephemeral loopback port for the real callback listener.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + listener.Addr().String() + "/callback"
	listener.Close()
	var challenge string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge || r.Form.Get("code") != "valid-code" || r.Form.Get("redirect_uri") != redirect || r.Form.Get("client_id") != "own-app" || r.Form.Get("grant_type") != "authorization_code" {
			t.Error("bad PKCE exchange")
		}
		fmt.Fprint(w, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`)
	}))
	defer tokenServer.Close()
	cfg := config{ClientID: "own-app", RedirectURI: redirect, TokenFile: filepath.Join(t.TempDir(), "tokens.json"), RequestTimeoutSeconds: 1}
	a, err := newAuth(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a.tokenURL = tokenServer.URL
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = a.authorizeWithBrowser(ctx, func(address string) error {
		u, err := url.Parse(address)
		if err != nil {
			return err
		}
		q := u.Query()
		challenge = q.Get("code_challenge")
		if q.Get("scope") != scopes || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("response_type") != "code" {
			t.Error("bad authorize URL")
		}
		for _, state := range []string{"wrong-state", q.Get("state")} {
			res, err := http.Get(redirect + "?" + url.Values{"state": {state}, "code": {"valid-code"}}.Encode())
			if err != nil {
				return err
			}
			res.Body.Close()
			wantStatus := 200
			if state == "wrong-state" {
				wantStatus = 400
			}
			if res.StatusCode != wantStatus {
				t.Errorf("state %s got %d", state, res.StatusCode)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newAuth(cfg)
	if err != nil || reloaded.token.RefreshToken != "private-refresh" {
		t.Fatalf("cache missing: %v", err)
	}
	// A second save replaces the file, exercising refresh-token cache replacement.
	if err := a.saveToken(a.token); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(cfg.TokenFile)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("token file must be private")
		}
	}
}
