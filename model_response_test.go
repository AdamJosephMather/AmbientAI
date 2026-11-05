package main

import (
	"encoding/json"
	"testing"
)

func TestParseModelResponse(t *testing.T) {
	for _, tc := range []struct {
		name, input, speech, body string
		wantError                 bool
	}{
		{name: "silence", input: `{"speech":null,"window_add":[],"invoke_worker":false}`},
		{name: "escaped markdown", input: `{"speech":"Here's a summary.","window_add":[{"title":"Notes","body":"**Bold**\n\n- Item\n\tIndented"}],"invoke_worker":false}`, speech: "Here's a summary.", body: "**Bold**\n\n- Item\n\tIndented"},
		{name: "literal newlines", input: "{\n\"speech\":null,\n\"window_add\":[{\"title\":\"Notes\",\"body\":\"**Bold**\n\n- Item\r\n\tIndented\"}],\n\"invoke_worker\":false\n}", body: "**Bold**\n\n- Item\r\n\tIndented"},
		{name: "existing escapes", input: `{"speech":"He said \"hello\" at C:\\notes.","window_add":[{"title":"Notes","body":"A quoted \"word\" and literal \\n` + "\n" + `next line"}],"invoke_worker":false}`, speech: `He said "hello" at C:\notes.`, body: "A quoted \"word\" and literal \\n\nnext line"},
		{name: "truncated", input: `{"speech":"unfinished`, wantError: true},
		{name: "trailing comma", input: `{"speech":null,}`, wantError: true},
		{name: "wrong type", input: `{"invoke_worker":"false"}`, wantError: true},
		{name: "null object", input: `null`, wantError: true},
		{name: "extra object", input: `{} {}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseModelResponse(tc.input)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError = %v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			if got.Speech != tc.speech {
				t.Fatalf("speech = %q, want %q", got.Speech, tc.speech)
			}
			if tc.body != "" && (len(got.WindowAdd) != 1 || got.WindowAdd[0].Body != tc.body) {
				t.Fatalf("windows = %#v, want body %q", got.WindowAdd, tc.body)
			}
			encoded, err := json.Marshal(got)
			if err != nil || !json.Valid(encoded) {
				t.Fatal("parsed response must produce valid JSON for history")
			}
		})
	}
}
