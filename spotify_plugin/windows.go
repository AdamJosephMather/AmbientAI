package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const playbackWindowTitle = "Spotify: Currently Playing"

type playbackWindows struct {
	mu        sync.Mutex
	ids       map[int]bool
	body      string
	lastKnown string
	send      func(message) error
	wake      chan struct{}
}

func newPlaybackWindows(send func(message) error) *playbackWindows {
	return &playbackWindows{ids: make(map[int]bool), body: "Loading Spotify playback…", send: send, wake: make(chan struct{}, 1)}
}

func (w *playbackWindows) active() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.ids) > 0
}

func (w *playbackWindows) lifecycle(request message) error {
	var params struct {
		ID   *int   `json:"window_id"`
		Type string `json:"window_type"`
	}
	if len(request.ID) != 0 || len(request.Result) != 0 || request.Error != nil || json.Unmarshal(request.Params, &params) != nil || params.ID == nil {
		return fmt.Errorf("invalid window lifecycle notification")
	}
	if params.Type != "currently_playing" {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if request.Method == "windows/deleted" {
		delete(w.ids, *params.ID)
		if len(w.ids) == 0 {
			w.body, w.lastKnown = "Loading Spotify playback…", ""
		}
		return nil
	}
	w.ids[*params.ID] = true
	if err := w.sendWindow(*params.ID); err != nil {
		return err
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

// Hold mu through sends so processing deletion also stops subsequent updates.
func (w *playbackWindows) sendWindow(id int) error {
	params, _ := json.Marshal(map[string]any{"window_id": id, "title": playbackWindowTitle, "markdown": w.body})
	return w.send(message{JSONRPC: "2.0", Method: "plugin/updatewindow", Params: params})
}

func (w *playbackWindows) update(p playback, err error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.ids) == 0 {
		return nil
	}
	var body string
	if err != nil {
		body = "Unable to refresh Spotify playback.\n\n" + markdownText(err.Error())
		if w.lastKnown != "" {
			body = w.lastKnown + "\n\n*Last known state; refresh failed.*\n\n" + markdownText(err.Error())
		}
	} else {
		body = playbackMarkdown(p)
		w.lastKnown = body
	}
	if body == w.body {
		return nil
	}
	w.body = body
	for id := range w.ids {
		if err := w.sendWindow(id); err != nil {
			return err
		}
	}
	return nil
}

func playbackMarkdown(p playback) string {
	if p.Song == nil {
		return "No song is currently available on Spotify."
	}
	state := "Paused"
	if p.Playing {
		state = "Playing"
	}
	body := "**" + markdownText(p.Song.Name) + "**"
	var artists []string
	for _, artist := range p.Song.Artists {
		artists = append(artists, markdownText(artist.Name))
	}
	if len(artists) > 0 {
		body += "\n\n" + strings.Join(artists, ", ")
	}
	return body + "\n\n" + state
}

// Treat Spotify metadata and error messages as literal text, including newlines.
func markdownText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	var out strings.Builder
	for _, r := range text {
		if strings.ContainsRune("\\`*_{}[]<>()#+-.!|~&", r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}
