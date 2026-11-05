package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompletionResponseFormat(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		format, present := request["response_format"]
		if requests == 0 {
			var value map[string]string
			if err := json.Unmarshal(format, &value); err != nil || value["type"] != "json_object" {
				t.Errorf("JSON output mode missing: %s (%v)", format, err)
			}
		} else if present {
			t.Error("ordinary worker requests should omit response_format")
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	chat := Chat{Config: ChatConfig{URL: server.URL}, Client: server.Client()}
	for _, options := range []CompletionOptions{
		{ResponseFormat: json.RawMessage(`{"type":"json_object"}`)},
		{},
	} {
		if _, err := chat.CompleteMessage(context.Background(), []Message{{Role: "user", Content: "Hello"}}, "test", "none", options); err != nil {
			t.Fatal(err)
		}
	}
}
