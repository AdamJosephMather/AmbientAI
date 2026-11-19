package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocol(t *testing.T) {
	input := strings.NewReader(`{"jsonrpc":"2.0","id":"register","result":{"registered":true}}
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","target":"back_end","arguments":{"query":"success"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","target":"back_end","arguments":{"query":"fail"}}}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","target":"front_end","arguments":{"query":"test"}}}
`)
	var output bytes.Buffer
	calls := 0
	err := serve(input, &output, func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
		calls++
		if strings.Contains(string(args), "fail") {
			return nil, errors.New("upstream test failure")
		}
		return json.RawMessage(`{"results":[]}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var registrationMessage message
	if err := decoder.Decode(&registrationMessage); err != nil || registrationMessage.Method != "plugin/register" || !strings.Contains(string(registrationMessage.Params), `"name":"Brave"`) {
		t.Fatalf("missing registration: %+v, %v", registrationMessage, err)
	}
	for id := 1; id <= 3; id++ {
		var response message
		if err := decoder.Decode(&response); err != nil {
			t.Fatal(err)
		}
		wantID, _ := json.Marshal(id)
		if string(response.ID) != string(wantID) || response.JSONRPC != "2.0" {
			t.Fatalf("incorrect response: %+v", response)
		}
		if id == 1 && string(response.Result) != `{"results":[]}` {
			t.Fatalf("missing search result: %+v", response)
		}
		if id > 1 && (response.Error == nil || len(response.Result) != 0) {
			t.Fatalf("missing error response: %+v", response)
		}
	}
	if calls != 2 {
		t.Fatalf("frontend was allowed to search: calls=%d", calls)
	}
}

func TestConfig(t *testing.T) {
	t.Setenv("BRAVE_SEARCH_CONFIG", "")
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	directory := t.TempDir()
	executable := filepath.Join(directory, "plugin.exe")
	cfg, err := loadConfig(executable)
	if err != nil || cfg.APIKey != "" || cfg.RequestTimeoutSeconds != 20 || cfg.SafeSearch != "moderate" {
		t.Fatalf("incorrect defaults: %+v, %v", cfg, err)
	}
	path := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(path, []byte("api_key = 'file-secret'\ncountry = 'CA'\nrequest_timeout_seconds = 12\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(executable)
	if err != nil || cfg.APIKey != "file-secret" || cfg.Country != "CA" || cfg.RequestTimeoutSeconds != 12 {
		t.Fatal("config beside executable was not loaded")
	}
	t.Setenv("BRAVE_SEARCH_API_KEY", "environment-secret")
	cfg, err = loadConfig(executable)
	if err != nil || cfg.APIKey != "environment-secret" {
		t.Fatal("environment key did not override file")
	}
	t.Setenv("BRAVE_SEARCH_CONFIG", filepath.Join(directory, "missing.toml"))
	if _, err := loadConfig(executable); err == nil {
		t.Fatal("missing explicitly configured file ignored")
	}
	t.Setenv("BRAVE_SEARCH_CONFIG", path)
	for _, body := range []string{"api_key = unquoted-secret", "request_timeout_seconds = 0", "safesearch = 'unknown'"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(executable); err == nil || strings.Contains(err.Error(), "unquoted-secret") {
			t.Fatalf("config error missing or exposed key: %v", err)
		}
	}
}
