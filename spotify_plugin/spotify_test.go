package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *spotify {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	cfg := config{ClientID: "test-client", TokenFile: filepath.Join(t.TempDir(), "tokens.json"), DeviceName: "My PC", DeviceWaitSeconds: 1, CallTimeoutSeconds: 3, RequestTimeoutSeconds: 1}
	a := &auth{cfg: cfg, http: server.Client(), tokenURL: server.URL + "/token", token: token{AccessToken: "access", RefreshToken: "refresh", ClientID: cfg.ClientID, ExpiresAt: time.Now().Add(time.Hour)}}
	s := newSpotify(cfg, a)
	s.baseURL = server.URL
	s.devicePoll = time.Millisecond
	s.launch = func() error { t.Error("unexpected Spotify launch"); return fmt.Errorf("unexpected launch") }
	return s
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestPlaySearchKeepsPlayingDevice(t *testing.T) {
	for _, tc := range []struct{ args, query string }{
		{`{"song_name":"Can't Sleep Love","artist_name":"Pentatonix"}`, `track:"Can't Sleep Love" artist:"Pentatonix"`},
		{`{"song_name":"Hello"}`, `track:"Hello"`},
		{`{"artist_name":"Adele"}`, `artist:"Adele"`},
	} {
		t.Run(tc.query, func(t *testing.T) {
			var plays atomic.Int32
			s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/search":
					if r.URL.Query().Get("q") != tc.query || r.URL.Query().Get("type") != "track" {
						t.Errorf("unexpected query: %s", r.URL.RawQuery)
					}
					fmt.Fprint(w, `{"tracks":{"items":[{"uri":"spotify:track:blocked","is_playable":false},{"uri":"spotify:track:match","name":"Matched song","album":{"uri":"spotify:album:matched-album"}}]}}`)
				case "/me/player":
					fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote","name":"Kitchen","type":"speaker"},"item":{"uri":"spotify:track:match"}}`)
				case "/me/player/play":
					plays.Add(1)
					if r.Method != "PUT" || r.URL.Query().Get("device_id") != "remote" {
						t.Errorf("playback moved: %s %s", r.Method, r.URL)
					}
					var body struct {
						Context string `json:"context_uri"`
						Offset  struct {
							URI string `json:"uri"`
						} `json:"offset"`
						URIs []string `json:"uris"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.Context != "spotify:album:matched-album" || body.Offset.URI != "spotify:track:match" || len(body.URIs) != 0 {
						t.Errorf("bad play body: %+v", body)
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					w.WriteHeader(500)
				}
			})
			if _, err := s.call(context.Background(), "play", json.RawMessage(tc.args)); err != nil {
				t.Fatal(err)
			}
			if plays.Load() != 1 {
				t.Fatal("expected one play")
			}
		})
	}
}

func TestResumeLaunchesAndWaitsOnlyForLocal(t *testing.T) {
	for _, name := range []string{"resume", "play"} {
		t.Run(name, func(t *testing.T) {
			var launched atomic.Bool
			var discoveries atomic.Int32
			var plays atomic.Int32
			s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/me/player":
					if plays.Load() > 0 {
						fmt.Fprint(w, `{"is_playing":true,"device":{"id":"local"}}`)
					} else {
						fmt.Fprint(w, `{"is_playing":false,"device":{"id":"remote","name":"Kitchen"}}`)
					}
				case "/me/player/devices":
					count := discoveries.Add(1)
					if launched.Load() && count >= 3 {
						fmt.Fprint(w, `{"devices":[{"id":"remote","name":"Kitchen","type":"speaker"},{"id":"local","name":"my pc","type":"Computer"}]}`)
					} else {
						fmt.Fprint(w, `{"devices":[{"id":"remote","name":"Kitchen","type":"speaker"}]}`)
					}
				case "/me/player/play":
					plays.Add(1)
					if r.URL.Query().Get("device_id") != "local" || !launched.Load() {
						t.Errorf("wrong target: %s", r.URL)
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					w.WriteHeader(500)
				}
			})
			s.launch = func() error {
				if launched.Swap(true) {
					t.Error("launched twice")
				}
				return nil
			}
			if _, err := s.call(context.Background(), name, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			if discoveries.Load() < 3 || plays.Load() != 1 {
				t.Fatal("did not wait for local device")
			}
		})
	}
}

func TestRemotePlaybackStartingDuringDiscoveryIsPreserved(t *testing.T) {
	var reads atomic.Int32
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/player":
			writeJSON(w, playback{Playing: reads.Add(1) > 1, Device: device{ID: "remote", Name: "Phone"}})
		case "/me/player/devices":
			fmt.Fprint(w, `{"devices":[{"id":"local","name":"My PC","type":"computer"}]}`)
		case "/me/player/play":
			if r.URL.Query().Get("device_id") != "remote" {
				t.Error("interrupted remote session")
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(500)
		}
	})
	if _, err := s.call(context.Background(), "resume", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestMissingLocalNeverPlaysRemote(t *testing.T) {
	var launched atomic.Int32
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/player":
			w.WriteHeader(204)
		case "/me/player/devices":
			fmt.Fprint(w, `{"devices":[{"id":"remote","name":"Other PC","type":"computer"}]}`)
		default:
			t.Errorf("must not play/transfer: %s", r.URL)
			w.WriteHeader(500)
		}
	})
	s.launch = func() error { launched.Add(1); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := s.call(ctx, "resume", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing device succeeded")
	}
	if launched.Load() != 1 {
		t.Fatal("expected one launch")
	}
}

func TestLocalDeviceMatching(t *testing.T) {
	s := &spotify{deviceName: "My PC"}
	for _, tc := range []struct {
		name    string
		devices []device
		wantID  string
		wantErr bool
	}{
		{"match", []device{{ID: "local", Name: "my pc", Type: "computer"}}, "local", false},
		{"wrong type", []device{{ID: "remote", Name: "My PC", Type: "speaker"}}, "", false},
		{"restricted", []device{{ID: "local", Name: "My PC", Type: "computer", Restricted: true}}, "", true},
		{"ambiguous", []device{{ID: "one", Name: "My PC", Type: "computer"}, {ID: "two", Name: "My PC", Type: "computer"}}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, found, err := s.localDevice(tc.devices)
			if (err != nil) != tc.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantErr && (d.ID != tc.wantID || found != (tc.wantID != "")) {
				t.Fatalf("bad match: %+v %v", d, found)
			}
		})
	}
	s.cfg.DeviceID = "exact"
	d, found, err := s.localDevice([]device{{ID: "exact", Name: "Renamed", Type: "computer"}, {ID: "wrong", Name: "My PC", Type: "computer"}})
	if err != nil || !found || d.ID != "exact" {
		t.Fatalf("explicit ID failed: %+v %v", d, err)
	}
}

func TestControlsDoNotLaunchOrMovePlayback(t *testing.T) {
	for _, tc := range []struct{ name, method, endpoint string }{{"pause", "PUT", "pause"}, {"next_song", "POST", "next"}, {"prev_song", "POST", "previous"}} {
		t.Run(tc.name, func(t *testing.T) {
			var commands atomic.Int32
			s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/me/player" {
					fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"}}`)
					return
				}
				commands.Add(1)
				if r.URL.Path != "/me/player/"+tc.endpoint || r.Method != tc.method || r.URL.Query().Get("device_id") != "remote" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(204)
			})
			if _, err := s.call(context.Background(), tc.name, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			if commands.Load() != 1 {
				t.Fatal("expected exactly one command")
			}
		})
	}
}

func TestValidationAndNoSearchMatchHaveNoPlaybackSideEffects(t *testing.T) {
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("unexpected side effect: %s", r.URL)
		}
		fmt.Fprint(w, `{"tracks":{"items":[]}}`)
	})
	for _, tc := range []struct{ name, args string }{
		{"play", `null`}, {"play", `[]`}, {"play", `{"song_name":null}`}, {"play", `{"song_name":" "}`}, {"play", `{"artist_name":4}`}, {"play", `{"unknown":"hello"}`}, {"pause", `{"song_name":"hello"}`}, {"unknown", `{}`}, {"play", `{"song_name":"no match"}`},
	} {
		if _, err := s.call(context.Background(), tc.name, json.RawMessage(tc.args)); err == nil {
			t.Errorf("accepted %s %s", tc.name, tc.args)
		}
	}
}

func TestUnauthorizedRefreshAndRateLimit(t *testing.T) {
	var requests, refreshes atomic.Int32
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			refreshes.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" || r.Form.Get("client_id") != "test-client" {
				t.Error("bad refresh request")
			}
			fmt.Fprint(w, `{"access_token":"replacement","refresh_token":"rotated","expires_in":3600}`)
			return
		}
		requests.Add(1)
		if r.Header.Get("Authorization") == "Bearer access" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(429)
		fmt.Fprint(w, `private server error text`)
	})
	_, err := s.current(context.Background())
	if err == nil || !strings.Contains(err.Error(), "20 seconds") || strings.Contains(err.Error(), "private") {
		t.Fatalf("bad error: %v", err)
	}
	_, _ = s.current(context.Background())
	if requests.Load() != 2 || refreshes.Load() != 1 {
		t.Fatal("did not refresh once or honor rate cooldown")
	}
	a, err := newAuth(s.cfg)
	if err != nil || a.token.RefreshToken != "rotated" || a.token.AccessToken != "replacement" {
		t.Fatalf("rotated token not persisted: %v", err)
	}
}

func TestSongUpdatesIgnorePauseProgressAndFailedPolls(t *testing.T) {
	var reads atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch n := reads.Add(1); n {
		case 1:
			fmt.Fprint(w, `{"is_playing":true,"item":{"uri":"spotify:track:a"},"progress_ms":10}`)
		case 2:
			fmt.Fprint(w, `{"is_playing":false,"item":{"uri":"spotify:track:a"},"progress_ms":50}`)
		case 3:
			w.WriteHeader(503)
		case 4:
			fmt.Fprint(w, `{"is_playing":true,"item":{"uri":"spotify:track:b"}}`)
		default:
			w.WriteHeader(204)
		}
	})
	// Zero interval makes the deterministic fake sequence immediate, without sleeps.
	var changes []string
	s.updates(ctx, func(p playback) error {
		if p.Song == nil {
			changes = append(changes, "none")
			cancel()
		} else {
			changes = append(changes, p.Song.URI)
		}
		return nil
	})
	if strings.Join(changes, ",") != "spotify:track:b,none" {
		t.Fatalf("unexpected updates: %v", changes)
	}
}

func TestPlayerErrorPreservesSpecificReason(t *testing.T) {
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me/player" {
			fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"},"actions":{"disallows":{"skipping_next":true}}}`)
			return
		}
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":{"status":403,"message":"Player command failed: Restriction violated","reason":"RESTRICTION_VIOLATED"},"private":"must not expose"}`)
	})
	_, err := s.call(context.Background(), "next_song", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected rejection")
	}
	for _, want := range []string{"POST /me/player/next", "HTTP 403", "Restriction violated", "RESTRICTION_VIOLATED"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Premium") || strings.Contains(err.Error(), "must not expose") {
		t.Fatalf("misleading or unfiltered error: %v", err)
	}
}

func TestPlaybackIncludesContextAndActionRestrictions(t *testing.T) {
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"context":{"type":"playlist","uri":"spotify:playlist:test"},"actions":{"disallows":{"skipping_prev":true}}}`)
	})
	p, err := s.current(context.Background())
	if err != nil || p.Context == nil || p.Context.Type != "playlist" || !strings.Contains(string(p.Actions), `"skipping_prev":true`) {
		t.Fatalf("missing context/restrictions: %+v %v", p, err)
	}
}

func TestAPIErrorRedactsTokenAndIgnoresNonJSON(t *testing.T) {
	for _, body := range []string{`{"error":{"message":"bad access\nmessage"}}`, `<html>private debug content</html>`} {
		s := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); fmt.Fprint(w, body) })
		_, err := s.current(context.Background())
		if err == nil || strings.Contains(err.Error(), "access") || strings.Contains(err.Error(), "private debug") || strings.Contains(err.Error(), "\n") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestSearchPlaybackFallsBackWhenAlbumUnavailable(t *testing.T) {
	for _, album := range []string{"", `,"album":{"uri":"spotify:playlist:unexpected"}`} {
		s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search":
				fmt.Fprint(w, `{"tracks":{"items":[{"uri":"spotify:track:match"`+album+`}]}}`)
			case "/me/player":
				fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"},"item":{"uri":"spotify:track:match"}}`)
			case "/me/player/play":
				var body struct {
					URIs    []string `json:"uris"`
					Context string   `json:"context_uri"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.URIs) != 1 || body.URIs[0] != "spotify:track:match" || body.Context != "" {
					t.Errorf("invalid fallback: %+v", body)
				}
				w.WriteHeader(204)
			default:
				t.Errorf("unexpected request: %s", r.URL)
				w.WriteHeader(500)
			}
		})
		if _, err := s.call(context.Background(), "play", json.RawMessage(`{"song_name":"test"}`)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlayWaitsForRequestedSongInsteadOfReturningPreviousSong(t *testing.T) {
	var plays, afterReads atomic.Int32
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, `{"tracks":{"items":[{"uri":"spotify:track:new","name":"New song","artists":[{"name":"New artist"}]}]}}`)
		case "/me/player/play":
			plays.Add(1)
			w.WriteHeader(204)
		case "/me/player":
			if plays.Load() > 0 && afterReads.Add(1) >= 2 {
				fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"},"item":{"uri":"spotify:track:new","name":"New song","artists":[{"name":"New artist"}]}}`)
			} else {
				fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"},"item":{"uri":"spotify:track:old","name":"Old song"}}`)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(500)
		}
	})
	result, err := s.call(context.Background(), "play", json.RawMessage(`{"song_name":"New song"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := result.(songResult)
	if got.SongName == nil || *got.SongName != "New song" || len(got.ArtistNames) != 1 || got.ArtistNames[0] != "New artist" || got.IsPlaying == nil || !*got.IsPlaying {
		t.Fatalf("returned stale or unconfirmed song: %+v", got)
	}
	if plays.Load() != 1 || afterReads.Load() != 2 {
		t.Fatal("should poll state without repeating playback command")
	}
}

func TestAcceptedPlayWithUnconfirmedStateNeverReturnsOldSongOrRetries(t *testing.T) {
	for _, stateFails := range []bool{false, true} {
		t.Run(fmt.Sprint("stateFails=", stateFails), func(t *testing.T) {
			var plays atomic.Int32
			s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/search":
					fmt.Fprint(w, `{"tracks":{"items":[{"uri":"spotify:track:new","name":"New song","artists":[{"name":"New artist"}]}]}}`)
				case "/me/player/play":
					plays.Add(1)
					w.WriteHeader(204)
				case "/me/player":
					if stateFails && plays.Load() > 0 {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"},"item":{"uri":"spotify:track:old","name":"Old song"}}`)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					w.WriteHeader(500)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			result, err := s.call(ctx, "play", json.RawMessage(`{"song_name":"New song"}`))
			if err != nil {
				t.Fatalf("accepted play became an error: %v", err)
			}
			got := result.(songResult)
			if got.SongName == nil || *got.SongName != "New song" || got.IsPlaying != nil || plays.Load() != 1 {
				t.Fatalf("wrong pending result or repeated command: %+v", got)
			}
			data, err := json.Marshal(got)
			if err != nil || strings.Contains(string(data), "uri") || strings.Contains(string(data), "Old song") {
				t.Fatalf("leaked stale state/metadata: %s %v", data, err)
			}
		})
	}
}
