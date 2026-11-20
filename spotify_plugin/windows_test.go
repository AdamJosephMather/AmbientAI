package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func windowEvent(method string, id int) message {
	return message{JSONRPC: "2.0", Method: method, Params: json.RawMessage(fmt.Sprintf(`{"window_id":%d,"window_type":"currently_playing"}`, id))}
}

func TestPlaybackWindowsStateAndDeletion(t *testing.T) {
	var sent []message
	w := newPlaybackWindows(func(m message) error { sent = append(sent, m); return nil })
	for _, id := range []int{7, 9} {
		if err := w.lifecycle(windowEvent("windows/created", id)); err != nil {
			t.Fatal(err)
		}
	}
	p := playback{Playing: true, Song: &track{Name: "A *song* [title]\n# heading"}}
	if err := json.Unmarshal([]byte(`[{"name":"An Artist"}]`), &p.Song.Artists); err != nil {
		t.Fatal(err)
	}
	if err := w.update(p, nil); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 4 {
		t.Fatalf("expected updates for both windows, got %d", len(sent))
	}
	var view struct {
		ID       int `json:"window_id"`
		Title    string
		Markdown string
	}
	if err := json.Unmarshal(sent[2].Params, &view); err != nil {
		t.Fatal(err)
	}
	if view.Title != playbackWindowTitle || !strings.Contains(view.Markdown, `A \*song\* \[title\] \# heading`) || !strings.Contains(view.Markdown, "An Artist\n\nPlaying") {
		t.Fatalf("bad playback view: %+v", view)
	}
	w.update(p, nil)
	if len(sent) != 4 {
		t.Fatal("unchanged state sent redundant updates")
	}
	p.Playing = false
	w.update(p, nil)
	w.update(p, errors.New("offline"))
	if !strings.Contains(string(sent[len(sent)-1].Params), "Last known state") {
		t.Fatal("failure did not mark stale state")
	}
	w.update(p, nil)
	if strings.Contains(string(sent[len(sent)-1].Params), "Last known state") {
		t.Fatal("recovery did not clear stale warning")
	}
	w.lifecycle(windowEvent("windows/deleted", 7))
	before := len(sent)
	w.update(playback{}, nil)
	if len(sent) != before+1 {
		t.Fatal("deleted window still receives updates")
	}
	json.Unmarshal(sent[len(sent)-1].Params, &view)
	if view.ID != 9 || !strings.Contains(view.Markdown, "No song") {
		t.Fatalf("bad empty view: %+v", view)
	}
	w.lifecycle(windowEvent("windows/deleted", 9))
	before = len(sent)
	w.update(p, nil)
	if w.active() || len(sent) != before {
		t.Fatal("windows remain active after deletion")
	}
}

func TestProtocolCurrentlyPlayingWindowWithoutSongNotifications(t *testing.T) {
	var paused atomic.Bool
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/me/player" {
			t.Errorf("unexpected API operation: %s %s", r.Method, r.URL)
		}
		fmt.Fprintf(w, `{"is_playing":%t,"item":{"uri":"spotify:track:test","name":"Test","artists":[{"name":"Artist"}]}}`, !paused.Load())
	})
	s.cfg.PollIntervalSeconds = 1
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- serve(inReader, outWriter, s); outWriter.Close() }()
	t.Cleanup(func() {
		inWriter.Close()
		outReader.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("serve did not stop")
		}
	})
	lines := make(chan message, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(outReader)
		for scanner.Scan() {
			var m message
			if json.Unmarshal(scanner.Bytes(), &m) == nil {
				lines <- m
			}
		}
	}()
	read := func() message {
		t.Helper()
		select {
		case m, ok := <-lines:
			if !ok {
				t.Fatal("protocol closed")
			}
			return m
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for plugin")
			return message{}
		}
	}
	reg := read()
	if !strings.Contains(string(reg.Params), `"name":"currently_playing"`) {
		t.Fatal("window type not registered")
	}
	enc := json.NewEncoder(inWriter)
	enc.Encode(message{JSONRPC: "2.0", ID: json.RawMessage(`"register"`), Result: json.RawMessage(`{"registered":true}`)})
	enc.Encode(windowEvent("windows/created", 7))
	loading := read()
	if loading.Method != "plugin/updatewindow" || len(loading.ID) != 0 {
		t.Fatalf("lifecycle produced a reply: %+v", loading)
	}
	playing := read()
	if playing.Method != "plugin/updatewindow" || !strings.Contains(string(playing.Params), "Playing") || !strings.Contains(string(playing.Params), "Artist") {
		t.Fatalf("bad playing update: %+v", playing)
	}
	paused.Store(true)
	pause := read()
	if pause.Method != "plugin/updatewindow" || !strings.Contains(string(pause.Params), "Paused") {
		t.Fatalf("bad paused update: %+v", pause)
	}
	enc.Encode(windowEvent("windows/deleted", 7))
	enc.Encode(message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"get_current_song","target":"front_end","arguments":{}}`)})
	reply := read()
	if string(reply.ID) != "1" || reply.Error != nil {
		t.Fatalf("plugin stopped after window deletion: %+v", reply)
	}
}

func TestWindowLifecycleRejectsMalformedNotifications(t *testing.T) {
	w := newPlaybackWindows(func(message) error { return nil })
	for _, m := range []message{
		{Method: "windows/created", Params: json.RawMessage(`{"window_type":"currently_playing"}`)},
		{Method: "windows/created", Params: json.RawMessage(`{"window_id":null}`)},
		{Method: "windows/deleted", ID: json.RawMessage(`1`), Params: json.RawMessage(`{"window_id":7}`)},
	} {
		if w.lifecycle(m) == nil {
			t.Fatalf("accepted malformed lifecycle: %+v", m)
		}
	}
}
