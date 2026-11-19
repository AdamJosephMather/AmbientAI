package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProtocolRegistersSameToolsAndServesBothTargets(t *testing.T) {
	var lookups atomic.Int32
	s := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		if r.URL.Path != "/me/player" {
			t.Errorf("unexpected request %s", r.URL)
		}
		fmt.Fprint(w, `{"is_playing":true,"device":{"id":"remote"},"item":{"uri":"spotify:track:test","name":"Test","artists":[{"name":"Artist"}]}}`)
	})
	in := strings.NewReader(`{"jsonrpc":"2.0","id":"register","result":{"registered":true}}
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_current_song","target":"front_end","arguments":{}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_current_song","target":"back_end","arguments":{}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_current_song","target":"invalid","arguments":{}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"play","target":"front_end","arguments":{"song_name":null}}}
`)
	var out bytes.Buffer
	if err := serve(in, &out, s); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(&out)
	var messages []message
	for scanner.Scan() {
		var m message
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
	if len(messages) != 5 || messages[0].Method != "plugin/register" {
		t.Fatalf("unexpected protocol output: %+v", messages)
	}
	var registration struct {
		Name  string            `json:"name"`
		Front []json.RawMessage `json:"front_end_tools"`
		Back  []json.RawMessage `json:"back_end_tools"`
	}
	if json.Unmarshal(messages[0].Params, &registration) != nil || registration.Name != "Spotify" || len(registration.Front) != 6 || len(registration.Back) != 6 {
		t.Fatal("bad registration")
	}
	for i := range registration.Front {
		if !bytes.Equal(registration.Front[i], registration.Back[i]) {
			t.Fatal("frontend and backend tools differ")
		}
	}
	for i := 1; i <= 2; i++ {
		if string(messages[i].ID) != fmt.Sprint(i) || messages[i].Error != nil || !bytes.Contains(messages[i].Result, []byte(`"Artist"`)) {
			t.Fatalf("bad reply: %+v", messages[i])
		}
	}
	if messages[3].Error == nil || messages[4].Error == nil || lookups.Load() != 2 {
		t.Fatal("invalid calls were not rejected or disabled notifications polled")
	}
}

func TestProtocolRequiresPositiveAcknowledgement(t *testing.T) {
	s := &spotify{}
	for _, ack := range []string{`{"jsonrpc":"2.0","id":"register","result":{"registered":false}}`, `{"jsonrpc":"2.0","id":"wrong","result":{"registered":true}}`, `{"jsonrpc":"2.0","id":"register","result":{}}`} {
		var out bytes.Buffer
		if serve(strings.NewReader(ack), &out, s) == nil {
			t.Fatalf("accepted bad acknowledgement %s", ack)
		}
	}
}
