package main

import (
	"testing"
)

func TestParseModelResponseStripsStrayEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"plain json", `{"speech":"hi","tools":[]}`},
		{"envelope without timestamp", "[From: Zoe]\n\n{\"speech\":\"hi\",\"tools\":[]}"},
		{"envelope with stale timestamp", "[From: Zoe | 22:49]\n\n{\"speech\":\"hi\",\"tools\":[]}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := parseModelResponse(tc.content)
			if err != nil {
				t.Fatalf("parseModelResponse: %v", err)
			}
			if resp.Speech != "hi" {
				t.Errorf("speech = %q, want %q", resp.Speech, "hi")
			}
			if len(resp.Tools) != 0 {
				t.Errorf("tools = %v, want none", resp.Tools)
			}
		})
	}
}

func TestParseModelResponseInvalid(t *testing.T) {
	if _, err := parseModelResponse("not json"); err == nil {
		t.Fatal("expected error for non-JSON content")
	}
	if _, err := parseModelResponse("[From: Zoe]\n\nnot json"); err == nil {
		t.Fatal("expected error for non-JSON envelope body")
	}
}
