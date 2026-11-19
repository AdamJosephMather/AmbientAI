package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Active     bool   `json:"is_active"`
	Restricted bool   `json:"is_restricted"`
	Type       string `json:"type"`
}

type track struct {
	ID      string `json:"id"`
	URI     string `json:"uri"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	DurationMS int   `json:"duration_ms"`
	Playable   *bool `json:"is_playable,omitempty"`
	Album      *struct {
		URI string `json:"uri"`
	} `json:"album,omitempty"`
}

type playback struct {
	Playing    bool   `json:"is_playing"`
	Device     device `json:"device"`
	Song       *track `json:"item"`
	ProgressMS int    `json:"progress_ms"`
	Kind       string `json:"currently_playing_type"`
	Context    *struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"context"`
	Actions json.RawMessage `json:"actions,omitempty"`
}

// Model-facing results deliberately exclude Spotify's control identifiers and
// diagnostic metadata. A nil playing state means a command was accepted but
// confirmation is pending/unavailable, so callers must not infer failure.
type songResult struct {
	SongName    *string  `json:"song_name"`
	ArtistNames []string `json:"artist_names"`
	IsPlaying   *bool    `json:"is_playing"`
}

func summarizeSong(song *track, playing *bool) songResult {
	result := songResult{ArtistNames: []string{}, IsPlaying: playing}
	if song != nil {
		result.SongName = &song.Name
		for _, artist := range song.Artists {
			result.ArtistNames = append(result.ArtistNames, artist.Name)
		}
	}
	return result
}

// Spotify can acknowledge commands before GET /me/player reflects the change.
// Poll only state, never replay a command to obtain confirmation.
func (s *spotify) waitForPlayback(ctx context.Context, fallback *track, confirmed func(playback) bool) songResult {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		p, err := s.current(ctx)
		if err != nil {
			break
		}
		if confirmed(p) {
			return summarizeSong(p.Song, &p.Playing)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return summarizeSong(fallback, nil)
		case <-timer.C:
		}
	}
	return summarizeSong(fallback, nil)
}

func (s *spotify) playResult(ctx context.Context, selected *track, target device) songResult {
	return s.waitForPlayback(ctx, selected, func(p playback) bool {
		matches := selected == nil || (p.Song != nil && p.Song.URI == selected.URI)
		return p.Playing && p.Device.ID == target.ID && matches
	})
}

func (s *spotify) controlResult(ctx context.Context, command string, before playback) songResult {
	// No old song fallback: until a skip/pause is confirmed, the current song
	// is unknown. Keep the same compact result and its accepted/pending semantics.
	return s.waitForPlayback(ctx, nil, func(p playback) bool {
		if p.Device.ID != before.Device.ID {
			return false
		}
		if command == "pause" {
			return !p.Playing
		}
		if p.Song == nil || p.Song.URI == "" {
			return false
		}
		if before.Song == nil || p.Song.URI != before.Song.URI {
			return true
		}
		// Previous may restart the same track; next can also repeat the same
		// URI. A substantial position rewind confirms that observable change.
		return before.ProgressMS-p.ProgressMS > 1000
	})
}

type apiError struct {
	Status     int
	RetryAfter int
	Message    string
	Reason     string
	Endpoint   string
}

func (e *apiError) Error() string {
	if e.Status != 429 && (e.Message != "" || e.Reason != "") {
		detail := e.Message
		if e.Reason != "" {
			detail += " (reason: " + e.Reason + ")"
		}
		return fmt.Sprintf("Spotify %s failed (HTTP %d): %s", e.Endpoint, e.Status, strings.TrimSpace(detail))
	}
	switch e.Status {
	case 401:
		return "Spotify authorization expired; run --authorize again"
	case 403:
		return fmt.Sprintf("Spotify %s was forbidden (HTTP 403); Spotify returned no specific reason", e.Endpoint)
	case 404:
		return "Spotify has no usable playback context/device; open Spotify on this computer, select a song there, then retry"
	case 429:
		return fmt.Sprintf("Spotify rate limit reached; retry after %d seconds", e.RetryAfter)
	default:
		return fmt.Sprintf("Spotify API request failed (HTTP %d)", e.Status)
	}
}

type spotify struct {
	cfg        config
	auth       *auth
	baseURL    string
	launch     func() error
	deviceName string
	devicePoll time.Duration
	limitMu    sync.Mutex
	retryAt    time.Time
}

func newSpotify(cfg config, a *auth) *spotify {
	name := cfg.DeviceName
	if name == "" {
		name = localDeviceName()
	}
	return &spotify{cfg: cfg, auth: a, baseURL: "https://api.spotify.com/v1", launch: openSpotify, deviceName: name, devicePoll: time.Second}
}

func (s *spotify) api(ctx context.Context, method, path string, body any, out any) error {
	s.limitMu.Lock()
	remaining := time.Until(s.retryAt)
	s.limitMu.Unlock()
	if remaining > 0 {
		return &apiError{Status: 429, RetryAfter: int(remaining.Seconds()) + 1}
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	access, err := s.auth.accessToken(ctx, "")
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+access)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := s.auth.http.Do(req)
		if err != nil {
			return fmt.Errorf("Spotify API request failed or timed out")
		}
		if res.StatusCode == 401 && attempt == 0 {
			res.Body.Close()
			access, err = s.auth.accessToken(ctx, access)
			if err != nil {
				return err
			}
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			e := &apiError{Status: res.StatusCode, Endpoint: method + " " + strings.SplitN(path, "?", 2)[0]}
			// Keep Spotify's actual player rejection rather than guessing from the
			// status code. Only expose bounded, single-line standard error fields.
			var failure struct {
				Error struct {
					Message string `json:"message"`
					Reason  string `json:"reason"`
				} `json:"error"`
			}
			if json.NewDecoder(io.LimitReader(res.Body, 16*1024)).Decode(&failure) == nil {
				clean := func(v string) string {
					v = strings.ReplaceAll(v, access, "[redacted]")
					v = strings.Join(strings.Fields(v), " ")
					if len(v) > 512 {
						v = v[:512] + "..."
					}
					return v
				}
				e.Message, e.Reason = clean(failure.Error.Message), clean(failure.Error.Reason)
			}
			if res.StatusCode == 429 {
				e.RetryAfter, _ = strconv.Atoi(res.Header.Get("Retry-After"))
				if e.RetryAfter < 1 {
					e.RetryAfter = 5
				}
				// Bound untrusted headers before converting to a duration.
				if e.RetryAfter > 86400 {
					e.RetryAfter = 86400
				}
				s.limitMu.Lock()
				s.retryAt = time.Now().Add(time.Duration(e.RetryAfter) * time.Second)
				s.limitMu.Unlock()
			}
			res.Body.Close()
			return e
		}
		if out != nil && res.StatusCode != 204 {
			err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
			res.Body.Close()
			if err != nil {
				return fmt.Errorf("invalid Spotify API response")
			}
		} else {
			res.Body.Close()
		}
		return nil
	}
	return fmt.Errorf("Spotify authorization failed")
}

func (s *spotify) current(ctx context.Context) (playback, error) {
	var p playback
	err := s.api(ctx, "GET", "/me/player", nil, &p)
	return p, err
}

func (s *spotify) devices(ctx context.Context) ([]device, error) {
	var response struct {
		Devices []device `json:"devices"`
	}
	err := s.api(ctx, "GET", "/me/player/devices", nil, &response)
	return response.Devices, err
}

func usable(d device) error {
	if d.ID == "" || d.Restricted {
		return fmt.Errorf("Spotify device %q is unavailable or does not permit Web API control", d.Name)
	}
	return nil
}

func (s *spotify) localDevice(devices []device) (device, bool, error) {
	var matches []device
	for _, d := range devices {
		match := s.cfg.DeviceID != "" && d.ID == s.cfg.DeviceID
		if s.cfg.DeviceID == "" {
			match = s.deviceName != "" && strings.EqualFold(d.Name, s.deviceName) && strings.EqualFold(d.Type, "computer")
		}
		if match {
			matches = append(matches, d)
		}
	}
	if len(matches) > 1 {
		return device{}, false, fmt.Errorf("multiple Spotify devices match %q; configure device_id explicitly", s.deviceName)
	}
	if len(matches) == 0 {
		return device{}, false, nil
	}
	return matches[0], true, usable(matches[0])
}

// Always preserve an existing playing session, even when it starts while we are
// waiting for the local app. A paused remote device is never a fallback target.
func (s *spotify) playbackDevice(ctx context.Context) (device, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.DeviceWaitSeconds)*time.Second)
	defer cancel()
	launched := false
	for {
		p, err := s.current(ctx)
		if err != nil {
			return device{}, err
		}
		if p.Playing {
			return p.Device, usable(p.Device)
		}
		devices, err := s.devices(ctx)
		if err != nil {
			return device{}, err
		}
		if local, found, err := s.localDevice(devices); found || err != nil {
			if err != nil {
				return device{}, err
			}
			// Discovery takes time; recheck before choosing local playback.
			latest, err := s.current(ctx)
			if err != nil {
				return device{}, err
			}
			if latest.Playing {
				return latest.Device, usable(latest.Device)
			}
			return local, nil
		}
		if !launched {
			if err := s.launch(); err != nil {
				return device{}, err
			}
			launched = true
		}
		timer := time.NewTimer(s.devicePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return device{}, fmt.Errorf("this computer did not become available in Spotify Connect; install/open Spotify, sign in to the authorized account, and check device_name/device_id with --list-devices")
		case <-timer.C:
		}
	}
}

func searchQuery(song, artist string) string {
	quote := func(v string) string { return `"` + strings.NewReplacer(`"`, " ", `\`, " ").Replace(v) + `"` }
	var parts []string
	if song != "" {
		parts = append(parts, "track:"+quote(song))
	}
	if artist != "" {
		parts = append(parts, "artist:"+quote(artist))
	}
	return strings.Join(parts, " ")
}

func (s *spotify) search(ctx context.Context, song, artist string) (*track, error) {
	q := url.Values{"q": {searchQuery(song, artist)}, "type": {"track"}, "limit": {"10"}}
	var response struct {
		Tracks struct {
			Items []track `json:"items"`
		} `json:"tracks"`
	}
	if err := s.api(ctx, "GET", "/search?"+q.Encode(), nil, &response); err != nil {
		return nil, err
	}
	for _, t := range response.Tracks.Items {
		if strings.HasPrefix(t.URI, "spotify:track:") && (t.Playable == nil || *t.Playable) {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("no playable Spotify track matched the supplied song/artist")
}

func (s *spotify) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil || args == nil {
		return nil, fmt.Errorf("arguments must be an object")
	}
	if name != "play" && len(args) != 0 {
		return nil, fmt.Errorf("%s does not accept arguments", name)
	}
	var song, artist string
	if name == "play" {
		for key, value := range args {
			var v string
			if string(value) == "null" || json.Unmarshal(value, &v) != nil || strings.TrimSpace(v) == "" || len(v) > 300 {
				return nil, fmt.Errorf("%s must be a nonempty string of at most 300 bytes", key)
			}
			switch key {
			case "song_name":
				song = strings.TrimSpace(v)
			case "artist_name":
				artist = strings.TrimSpace(v)
			default:
				return nil, fmt.Errorf("unknown play argument %q", key)
			}
		}
	}
	switch name {
	case "get_current_song":
		p, err := s.current(ctx)
		return summarizeSong(p.Song, &p.Playing), err
	case "play", "resume":
		var selected *track
		var err error
		if song != "" || artist != "" {
			selected, err = s.search(ctx, song, artist)
			if err != nil {
				return nil, err
			}
		}
		d, err := s.playbackDevice(ctx)
		if err != nil {
			return nil, err
		}
		var body any
		if selected != nil {
			body = map[string]any{"uris": []string{selected.URI}}
			// A one-track URI list has no surrounding album tracks to navigate.
			// Start at the matched track in its album when Spotify supplies it.
			if selected.Album != nil && strings.HasPrefix(selected.Album.URI, "spotify:album:") {
				body = map[string]any{"context_uri": selected.Album.URI, "offset": map[string]string{"uri": selected.URI}}
			}
		}
		if err := s.api(ctx, "PUT", "/me/player/play?"+url.Values{"device_id": {d.ID}}.Encode(), body, nil); err != nil {
			return nil, err
		}
		return s.playResult(ctx, selected, d), nil
	case "pause", "next_song", "prev_song":
		p, err := s.current(ctx)
		if err != nil {
			return nil, err
		}
		if err := usable(p.Device); err != nil {
			return nil, err
		}
		method, endpoint := "POST", "next"
		if name == "prev_song" {
			endpoint = "previous"
		}
		if name == "pause" {
			method, endpoint = "PUT", "pause"
		}
		err = s.api(ctx, method, "/me/player/"+endpoint+"?"+url.Values{"device_id": {p.Device.ID}}.Encode(), nil, nil)
		if err != nil {
			return nil, err
		}
		return s.controlResult(ctx, name, p), nil
	default:
		return nil, fmt.Errorf("unknown Spotify tool")
	}
}

// Polling establishes a baseline without announcing the startup state. Failed
// polls never clear the last known song, avoiding false changes after outages.
func (s *spotify) updates(ctx context.Context, send func(playback) error) {
	interval := time.Duration(s.cfg.PollIntervalSeconds) * time.Second
	var previous string
	initialized := false
	for {
		p, err := s.current(ctx)
		if err == nil {
			key := ""
			if p.Song != nil {
				key = p.Song.URI
				if key == "" {
					data, _ := json.Marshal(p.Song)
					key = string(data)
				}
			}
			if initialized && key != previous {
				if send(p) != nil {
					return
				}
			}
			previous, initialized = key, true
		}
		wait := interval
		var rate *apiError
		if errors.As(err, &rate) && rate.Status == 429 && time.Duration(rate.RetryAfter)*time.Second > wait {
			wait = time.Duration(rate.RetryAfter) * time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
