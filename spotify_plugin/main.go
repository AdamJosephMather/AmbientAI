// Spotify plugin, based on example_plugin's NDJSON JSON-RPC transport.
// Stdout is exclusively protocol output unless an explicit CLI mode is used.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func registration() json.RawMessage {
	type tool struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"`
	}
	empty := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	play := map[string]any{"type": "object", "properties": map[string]any{
		"song_name":   map[string]any{"type": "string", "minLength": 1, "maxLength": 300, "description": "Song title; optional when artist_name is given."},
		"artist_name": map[string]any{"type": "string", "minLength": 1, "maxLength": 300, "description": "Artist name; alone selects a matching track by this artist."},
	}, "additionalProperties": false}
	tools := []tool{
		{"play", "Play the first playable Spotify search result matching song_name, artist_name, or both, starting at that track within its album when available so next/previous can navigate surrounding tracks. With neither, resume. Keep an already playing device; otherwise start only on this computer, launching Spotify and waiting if necessary. Briefly wait for Spotify to report the requested playback. Returns song_name, artist_names, and is_playing. A successful response with is_playing null means Spotify accepted the command but confirmation is pending, not failure; do not automatically retry play.", play},
		{"next_song", "Skip to the next song on the current Spotify device without moving playback or launching Spotify.", empty},
		{"prev_song", "Go to the previous song on the current Spotify device (Spotify may restart the current song). Do not move playback or launch Spotify.", empty},
		{"pause", "Pause playback on the current Spotify device without moving playback or launching Spotify.", empty},
		{"resume", "Resume Spotify on an already playing device; otherwise start only on this computer, launching Spotify and waiting if necessary. A successful response with is_playing null means Spotify accepted the command but confirmation is pending, not failure; do not automatically retry resume.", empty},
		{"get_current_song", "Get only song_name, artist_names, and is_playing, including playback on other devices. Song name is null when nothing is available. Does not launch Spotify.", empty},
	}
	data, _ := json.Marshal(map[string]any{"name": "Spotify", "version": "1.0.0", "front_end_tools": tools, "back_end_tools": tools})
	return data
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	authorize := flag.Bool("authorize", false, "Authorize your own Spotify developer app and save refresh credentials")
	list := flag.Bool("list-devices", false, "Print Spotify Connect device IDs and names, plus this computer's detected name")
	flag.Parse()
	if flag.NArg() != 0 || (*authorize && *list) {
		return fmt.Errorf("use no arguments for plugin mode, --authorize for setup, or --list-devices for diagnostics")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cfg, err := loadConfig(executable)
	if err != nil {
		return err
	}
	a, err := newAuth(cfg)
	if err != nil {
		return err
	}
	s := newSpotify(cfg, a)
	if *authorize {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := a.authorize(ctx); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Spotify authorization saved. You can now start the assistant.")
		return nil
	}
	if *list {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.CallTimeoutSeconds)*time.Second)
		defer cancel()
		devices, err := s.devices(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"detected_device_name": s.deviceName, "devices": devices})
	}
	return serve(os.Stdin, os.Stdout, s)
}

func serve(in io.Reader, out io.Writer, s *spotify) error {
	encoder := json.NewEncoder(out)
	var outputMu sync.Mutex
	send := func(value message) error { outputMu.Lock(); defer outputMu.Unlock(); return encoder.Encode(value) }
	if err := send(message{JSONRPC: "2.0", ID: json.RawMessage(`"register"`), Method: "plugin/register", Params: registration()}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	if !scanner.Scan() {
		return fmt.Errorf("registration acknowledgement missing")
	}
	var ack message
	var accepted struct {
		Registered bool `json:"registered"`
	}
	if json.Unmarshal(scanner.Bytes(), &ack) != nil || ack.JSONRPC != "2.0" || string(ack.ID) != `"register"` || ack.Error != nil || json.Unmarshal(ack.Result, &accepted) != nil || !accepted.Registered {
		return fmt.Errorf("registration rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var updates sync.WaitGroup
	defer func() { cancel(); updates.Wait() }()
	if s.cfg.SendSongUpdates {
		updates.Add(1)
		go func() {
			defer updates.Done()
			s.updates(ctx, func(p playback) error {
				text := "Spotify current song changed."
				var data any
				if p.Song == nil {
					text = "Spotify has no current song."
				} else {
					artists := make([]string, 0, len(p.Song.Artists))
					for _, artist := range p.Song.Artists {
						artists = append(artists, artist.Name)
					}
					data = map[string]any{"song_name": p.Song.Name, "artist_names": artists}
				}
				params, _ := json.Marshal(map[string]any{"message": text, "data": data})
				return send(message{JSONRPC: "2.0", Method: "plugin/notify", Params: params})
			})
		}()
	}
	for scanner.Scan() {
		var request message
		if json.Unmarshal(scanner.Bytes(), &request) != nil || request.JSONRPC != "2.0" || len(request.ID) == 0 {
			return fmt.Errorf("invalid host JSON-RPC request")
		}
		response := message{JSONRPC: "2.0", ID: request.ID}
		var params struct {
			Name      string          `json:"name"`
			Target    string          `json:"target"`
			Arguments json.RawMessage `json:"arguments"`
		}
		switch {
		case request.Method != "tools/call":
			response.Error = &rpcError{-32601, "Unknown method"}
		case json.Unmarshal(request.Params, &params) != nil:
			response.Error = &rpcError{-32602, "Invalid tool parameters"}
		case params.Target != "front_end" && params.Target != "back_end":
			response.Error = &rpcError{-32601, "Unknown target"}
		default:
			callCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.CallTimeoutSeconds)*time.Second)
			result, err := s.call(callCtx, params.Name, params.Arguments)
			cancel()
			if err != nil {
				response.Error = &rpcError{-32000, err.Error()}
			} else {
				response.Result, _ = json.Marshal(result)
			}
		}
		if err := send(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}
