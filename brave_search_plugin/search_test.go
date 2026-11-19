package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearchRequestAndResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("X-Subscription-Token") != "test-secret" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("incorrect request authentication or method")
		}
		for key, want := range map[string]string{"q": "cats & dogs", "count": "2", "offset": "1", "freshness": "pw", "extra_snippets": "true", "result_filter": "web", "text_decorations": "false", "country": "CA", "search_lang": "en", "safesearch": "moderate"} {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("%s = %q; want %q", key, got, want)
			}
		}
		_, _ = w.Write([]byte(`{"type":"search","query":{"original":"cats & dogs","altered":"cats and dogs","more_results_available":true},"web":{"results":[{"title":"Source","url":"https://example.com/source","description":"Snippet","extra_snippets":["More context"],"age":"2 days ago","page_age":"2026-11-16","language":"en","irrelevant":"not forwarded"}]}}`))
	}))
	defer server.Close()
	client := newSearchClient(config{APIKey: "test-secret", RequestTimeoutSeconds: 1, Country: "CA", SearchLanguage: "en", SafeSearch: "moderate"})
	client.endpoint = server.URL
	output, err := client.search(context.Background(), json.RawMessage(`{"query":" cats & dogs ","count":2,"offset":1,"freshness":"pw"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result searchOutput
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.Query != "cats & dogs" || result.AlteredQuery != "cats and dogs" || !result.MoreResultsAvailable || len(result.Results) != 1 || result.Results[0].URL != "https://example.com/source" || result.Results[0].PageAge != "2026-11-16" || len(result.Results[0].ExtraSnippets) != 1 {
		t.Fatalf("lost source information: %s", output)
	}
	if strings.Contains(string(output), "summary") || strings.Contains(string(output), "irrelevant") || strings.Contains(string(output), "test-secret") {
		t.Fatalf("unexpected output: %s", output)
	}
}

func TestSearchErrorsAndEmptyResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, want string
	}{
		{"auth", 401, "test-secret", "API key"},
		{"subscription", 403, "test-secret", "subscription"},
		{"rate-limit", 429, "test-secret", "rate limit"},
		{"upstream", 503, "test-secret", "HTTP 503"},
		{"bad-json", 200, "not JSON", "invalid search response"},
		{"error-body", 200, `{"type":"ErrorResponse","error":{"detail":"test-secret"}}`, "invalid search response"},
		{"empty", 200, `{"type":"search","query":{"original":"test"}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newSearchClient(config{APIKey: "test-secret", RequestTimeoutSeconds: 1, SafeSearch: "moderate"})
			client.endpoint = server.URL
			output, err := client.search(context.Background(), json.RawMessage(`{"query":"test"}`))
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "test-secret") {
					t.Fatalf("wrong error: %v", err)
				}
			} else if err != nil || !strings.Contains(string(output), `"results":[]`) {
				t.Fatalf("empty search: %s, %v", output, err)
			}
		})
	}
}

func TestSearchTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := newSearchClient(config{APIKey: "test-secret", RequestTimeoutSeconds: 1})
	client.endpoint = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.search(ctx, json.RawMessage(`{"query":"test"}`)); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout: %v", err)
	}
}

func TestSearchDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed a redirect with the API key") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	client := newSearchClient(config{APIKey: "test-secret", RequestTimeoutSeconds: 1})
	client.endpoint = server.URL
	if _, err := client.search(context.Background(), json.RawMessage(`{"query":"test"}`)); err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("expected redirect rejection: %v", err)
	}
}

func TestSearchArgumentValidation(t *testing.T) {
	for _, args := range []string{`null`, `{}`, `{"query":" "}`, `{"query":"test","count":0}`, `{"query":"test","count":21}`, `{"query":"test","offset":10}`, `{"query":"test","offset":-1}`, `{"query":"test","extra":"unknown"}`, `{"query":"test","freshness":"yesterday"}`, `{"query":"test","freshness":"2026-02-30to2026-03-01"}`, `{"query":"test","freshness":"2026-11-18to2026-11-01"}`} {
		if _, err := decodeArguments(json.RawMessage(args)); err == nil {
			t.Errorf("accepted invalid arguments: %s", args)
		}
	}
	for _, query := range []string{strings.Repeat("x", 601), strings.Repeat("word ", 76)} {
		raw, _ := json.Marshal(map[string]string{"query": query})
		if _, err := decodeArguments(raw); err == nil {
			t.Error("accepted oversized query")
		}
	}
	args, err := decodeArguments(json.RawMessage(`{"query":"test","freshness":"2026-11-01to2026-11-18"}`))
	if err != nil || *args.Count != 5 || args.Offset != 0 {
		t.Fatalf("wrong argument defaults: %+v, %v", args, err)
	}
}

func TestSearchOutputBound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "search", "web": map[string]any{"results": []map[string]string{{"description": strings.Repeat("x", 600*1024)}}}})
	}))
	defer server.Close()
	client := newSearchClient(config{APIKey: "test-secret", RequestTimeoutSeconds: 1})
	client.endpoint = server.URL
	if _, err := client.search(context.Background(), json.RawMessage(`{"query":"test"}`)); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized protocol result accepted: %v", err)
	}
}
