package main

import (
	"AmbientAI/openrouter"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as an actual stdio plugin so process lifecycle,
// protocol framing, and timeouts are exercised without network or AI calls.
func TestMain(m *testing.M) {
	if mode := os.Getenv("AMBIENT_PLUGIN_TEST_HELPER"); mode != "" {
		runPluginHelper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runPluginHelper(mode string) {
	if mode == "timeout" {
		time.Sleep(20 * time.Second)
		return
	}
	if mode == "invalid-json" {
		fmt.Println("not JSON")
		return
	}
	if mode == "bad-schema" {
		fmt.Println(`{"jsonrpc":"2.0","id":1,"method":"plugin/register","params":{"name":"Fixture","version":"1","front_end_tools":[{"name":"echo","description":"test","parameters":{"text":{"type":"string"}}}]}}`)
		return
	}
	enc := json.NewEncoder(os.Stdout)
	dec := json.NewDecoder(os.Stdin)
	reg := pluginRegistration{Name: "Fixture", Version: "1", FrontEndTools: []pluginTool{{Name: "echo", Description: "Echo test data", Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}}}
	reg.BackEndTools = append(reg.BackEndTools, reg.FrontEndTools...)
	if mode == "duplicate-tools" {
		reg.FrontEndTools = append(reg.FrontEndTools, reg.FrontEndTools[0])
	}
	if mode == "normalized-name" {
		reg.Name = "Fixture!"
	}
	data, _ := json.Marshal(reg)
	_ = enc.Encode(pluginMessage{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "plugin/register", Params: data})
	var ack pluginMessage
	if dec.Decode(&ack) != nil || string(ack.Result) != `{"registered":true}` {
		return
	}
	if mode == "notify" || mode == "bad-notify" || mode == "notify-burst" {
		count := 1
		if mode == "notify-burst" {
			count = 150
		}
		for i := 0; i < count; i++ {
			event := pluginMessage{JSONRPC: "2.0", Method: "plugin/notify", Params: json.RawMessage(`{"Plugin":"Impostor","message":"Song changed","data":{"track":"test"}}`)}
			if mode == "bad-notify" {
				event.ID = json.RawMessage(`99`)
			}
			_ = enc.Encode(event)
		}
	}
	for {
		var req pluginMessage
		if dec.Decode(&req) != nil {
			return
		}
		if mode == "slow" {
			time.Sleep(20 * time.Second)
		}
		if mode == "crash" {
			os.Exit(2)
		}
		response := pluginMessage{JSONRPC: "2.0", ID: req.ID}
		if mode == "rpc-error" {
			response.Error = &rpcError{Code: -32000, Message: "intentional"}
		} else {
			response.Result = req.Params
		}
		if mode == "bad-id" {
			response.ID = json.RawMessage(`99999`)
		}
		if mode == "notify" {
			_ = enc.Encode(pluginMessage{JSONRPC: "2.0", Method: "plugin/notify", Params: json.RawMessage(`{"message":"Update during tool call"}`)})
		}
		_ = enc.Encode(response)
	}
}

func loadHelper(t *testing.T, mode string, count int) (*PluginManager, string, string) {
	t.Helper()
	t.Setenv("AMBIENT_PLUGIN_TEST_HELPER", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, count)
	for i := range paths {
		paths[i] = exe
	}
	m, prompt, _, failures := LoadPlugins(&PluginConfig{Paths: paths, CallTimeoutSeconds: 1})
	t.Cleanup(m.Close)
	return m, prompt, failures
}

func TestPluginRoutingAndFrontendJSON(t *testing.T) {
	m, prompt, failures := loadHelper(t, "normal", 1)
	if failures != "" {
		t.Fatal(failures)
	}
	if !strings.Contains(prompt, `"tool":"Fixture_echo"`) || !strings.Contains(prompt, `"required":["text"]`) {
		t.Fatalf("incomplete frontend prompt: %s", prompt)
	}
	if len(m.WorkerTools()) != 1 || m.WorkerTools()[0].Function.Name != "Fixture_echo" {
		t.Fatal("missing namespaced backend tool")
	}
	previous := plugins
	plugins = m
	t.Cleanup(func() { plugins = previous })
	response, err := parseModelResponse(`{"speech":null,"tools":[{"tool":"Fixture_echo","text":"hello"},{"tool":"sleep"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	call, ok := response.Tools[0].Value.(PluginToolCall)
	if !ok || string(call.Arguments) != `{"text":"hello"}` {
		t.Fatalf("wrong parsed call: %#v", response)
	}
	encoded, _ := json.Marshal(response)
	if !strings.Contains(string(encoded), `{"text":"hello","tool":"Fixture_echo"}`) {
		t.Fatalf("call not preserved in history: %s", encoded)
	}
	for _, target := range []string{"front_end", "back_end"} {
		result, err := m.Call(context.Background(), target, call.Tool, call.Arguments)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Name, Target string
			Arguments    map[string]string
		}
		if json.Unmarshal([]byte(result), &got) != nil || got.Name != "echo" || got.Target != target || got.Arguments["text"] != "hello" {
			t.Fatalf("wrong plugin request: %s", result)
		}
	}
	if _, err := runToolCall(context.Background(), openrouter.FunctionCall{Name: "Fixture_echo", Arguments: `{"text":"worker"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := runToolCall(context.Background(), openrouter.FunctionCall{Name: "search", Arguments: `{"query":"removed"}`}); err == nil {
		t.Fatal("built-in search still available")
	}
}

func TestPluginRegistrationFailures(t *testing.T) {
	for _, mode := range []string{"invalid-json", "bad-schema", "duplicate-tools"} {
		t.Run(mode, func(t *testing.T) {
			m, _, failures := loadHelper(t, mode, 1)
			if failures == "" || len(m.front) != 0 || len(m.back) != 0 {
				t.Fatalf("invalid registration accepted: %s", failures)
			}
		})
	}
	t.Run("duplicate-plugin", func(t *testing.T) {
		m, _, failures := loadHelper(t, "normal", 2)
		if !strings.Contains(failures, "duplicate plugin namespace") || len(m.processes) != 1 {
			t.Fatalf("collision not rejected: %s", failures)
		}
	})
	t.Run("missing-executable", func(t *testing.T) {
		m, _, _, failures := LoadPlugins(&PluginConfig{Paths: []string{"missing-test-plugin.exe"}})
		defer m.Close()
		if !strings.Contains(failures, "Inform the user") {
			t.Fatal("missing actionable load error")
		}
	})
	t.Run("deadline", func(t *testing.T) {
		start := time.Now()
		_, _, failures := loadHelper(t, "timeout", 1)
		elapsed := time.Since(start)
		if !strings.Contains(failures, "registration timed out") || elapsed < 5*time.Second || elapsed > 8*time.Second {
			t.Fatalf("registration deadline failed (%s): %s", elapsed, failures)
		}
	})
}

func TestPluginCallFailures(t *testing.T) {
	for _, mode := range []string{"rpc-error", "crash", "bad-id", "slow"} {
		t.Run(mode, func(t *testing.T) {
			m, _, failures := loadHelper(t, mode, 1)
			if failures != "" {
				t.Fatal(failures)
			}
			start := time.Now()
			_, err := m.Call(context.Background(), "back_end", "Fixture_echo", json.RawMessage(`{}`))
			if err == nil {
				t.Fatal("expected tool failure")
			}
			if mode == "slow" {
				if time.Since(start) > 3*time.Second || !strings.Contains(err.Error(), "deadline exceeded") {
					t.Fatalf("wrong timeout: %v", err)
				}
				if _, err = m.Call(context.Background(), "back_end", "Fixture_echo", json.RawMessage(`{}`)); err == nil {
					t.Fatal("timed-out plugin remained active")
				}
			}
		})
	}
}

func TestConcurrentPluginCalls(t *testing.T) {
	m, _, failures := loadHelper(t, "normal", 1)
	if failures != "" {
		t.Fatal(failures)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := json.RawMessage(fmt.Sprintf(`{"text":"call-%d"}`, i))
			result, err := m.Call(context.Background(), "back_end", "Fixture_echo", args)
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			var response struct{ Arguments json.RawMessage }
			if json.Unmarshal([]byte(result), &response) != nil || string(response.Arguments) != string(args) {
				t.Errorf("call %d received another call's result: %s", i, result)
			}
		}(i)
	}
	wg.Wait()
}

func TestQueuedPluginCallCancellation(t *testing.T) {
	m, _, failures := loadHelper(t, "normal", 1)
	if failures != "" {
		t.Fatal(failures)
	}
	p := m.processes[0]
	p.gate <- struct{}{} // Hold the process as though another caller owns it.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := m.Call(ctx, "back_end", "Fixture_echo", json.RawMessage(`{}`))
	<-p.gate
	if err == nil || !strings.Contains(err.Error(), "failed waiting to run") {
		t.Fatalf("queued cancellation failed: %v", err)
	}
	if _, err := m.Call(context.Background(), "back_end", "Fixture_echo", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("queued timeout stopped a healthy plugin: %v", err)
	}
	m.Close()
	if _, err := m.Call(context.Background(), "back_end", "Fixture_echo", json.RawMessage(`{}`)); err == nil {
		t.Fatal("closed plugin accepted a call")
	}
}

func TestExamplePlugin(t *testing.T) {
	t.Setenv("EXAMPLE_PLUGIN_UPDATE_INTERVAL", "20ms")
	path := t.TempDir() + "/example.exe"
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = "example_plugin"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build example: %v\n%s", err, output)
	}
	m, _, _, failures := LoadPlugins(&PluginConfig{Paths: []string{path}})
	defer m.Close()
	if failures != "" {
		t.Fatal(failures)
	}
	select {
	case update := <-m.Updates():
		if update.Plugin != "Test" || update.Message != "Example plugin timer event" {
			t.Fatalf("unexpected example update: %+v", update)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("example plugin sent no idle update")
	}
	for _, tc := range []struct{ target, name, args, want string }{
		{"front_end", "Test_echo", `{"text":"hello"}`, `{"text":"hello"}`},
		{"back_end", "Test_add", `{"a":2,"b":3}`, `{"sum":5}`},
		{"back_end", "Test_wait", `{"seconds":0}`, `{"finished":true}`},
	} {
		result, err := m.Call(context.Background(), tc.target, tc.name, json.RawMessage(tc.args))
		if err != nil || result != tc.want {
			t.Fatalf("%s: got %s, %v", tc.name, result, err)
		}
	}
	if err := m.InformOfWindow(context.Background(), 42, "Test_test_window", false); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-m.WindowUpdates():
		if update.WindowID == nil || *update.WindowID != 42 || update.Title != "Window: 42" || update.Markdown != "Count: 0" {
			t.Fatalf("unexpected window update: %+v", update)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("example plugin sent no window update")
	}
	if err := m.InformOfWindow(context.Background(), 42, "Test_test_window", true); err != nil {
		t.Fatal(err)
	}
	p := m.windowtypes["Test_test_window"].process
	p.owndWindowsMu.Lock()
	owned := p.ownedWindows[42]
	p.owndWindowsMu.Unlock()
	if owned {
		t.Fatal("deleted window remains owned by plugin")
	}
	if _, err := m.Call(context.Background(), "back_end", "Test_fail", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "Intentional test failure") {
		t.Fatalf("missing test error: %v", err)
	}
}

func TestPluginNotifications(t *testing.T) {
	m, _, failures := loadHelper(t, "notify", 1)
	if failures != "" {
		t.Fatal(failures)
	}
	select {
	case update := <-m.Updates():
		if update.Plugin != "Fixture" || update.Message != "Song changed" || string(update.Data) != `{"track":"test"}` {
			t.Fatalf("wrong event attribution or data: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("idle notification did not arrive")
	}
	if _, err := m.Call(context.Background(), "back_end", "Fixture_echo", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-m.Updates():
		if update.Message != "Update during tool call" {
			t.Fatalf("unexpected event: %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("notification during tool call did not arrive")
	}
	m.Close()
	if _, ok := <-m.Updates(); ok {
		t.Fatal("updates channel did not close")
	}
}

func TestPluginNotificationFailures(t *testing.T) {
	t.Run("notification-with-id", func(t *testing.T) {
		m, _, failures := loadHelper(t, "bad-notify", 1)
		if failures != "" {
			t.Fatal(failures)
		}
		select {
		case <-m.processes[0].done:
		case <-time.After(time.Second):
			t.Fatal("invalid notification did not stop plugin")
		}
	})
	t.Run("burst-does-not-block-tools", func(t *testing.T) {
		m, _, failures := loadHelper(t, "notify-burst", 1)
		if failures != "" {
			t.Fatal(failures)
		}
		if _, err := m.Call(context.Background(), "back_end", "Fixture_echo", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("event queue blocked response: %v", err)
		}
	})
}

func TestPluginUpdateContextWhileSleeping(t *testing.T) {
	oldSleeping, oldPending, oldChat, oldWorkerChat := sleeping, workerPending, mainChat, mainChatForWorkers
	t.Cleanup(func() {
		sleeping, workerPending, mainChat, mainChatForWorkers = oldSleeping, oldPending, oldChat, oldWorkerChat
	})
	sleeping = true
	workerPending = false
	mainChat = nil
	mainChatForWorkers = nil
	deliverPluginUpdate(PluginUpdate{Plugin: "Spotify", Message: "Song changed", Data: json.RawMessage(`{"track":"test"}`)})
	if !sleeping || !workerPending || frontTurns.active() {
		t.Fatal("notification woke or interrupted the assistant")
	}
	if len(mainChat) != 1 || !strings.Contains(mainChat[0].Content, "[From: Plugin: Spotify | ") || !strings.Contains(mainChat[0].Content, `{"track":"test"}`) || len(mainChatForWorkers) != 1 {
		t.Fatalf("notification missing from conversation: %+v", mainChat)
	}
}

func TestBravePluginRegistrationAndMissingKey(t *testing.T) {
	path := t.TempDir() + "/brave.exe"
	configPath := t.TempDir() + "/config.toml"
	if err := os.WriteFile(configPath, []byte("api_key = ''\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRAVE_SEARCH_CONFIG", configPath)
	t.Setenv("BRAVE_SEARCH_API_KEY", "")
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = "brave_search_plugin"
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Brave plugin: %v\n%s", err, output)
	}
	m, prompt, _, failures := LoadPlugins(&PluginConfig{Paths: []string{path}})
	defer m.Close()
	if failures != "" || prompt != "" || len(m.WorkerTools()) != 1 || m.WorkerTools()[0].Function.Name != "Brave_search" {
		t.Fatalf("incorrect Brave registration: %s", failures)
	}
	if _, err := m.Call(context.Background(), "back_end", "Brave_search", json.RawMessage(`{"query":"test"}`)); err == nil || !strings.Contains(err.Error(), "Brave API key is missing") {
		t.Fatalf("missing configuration error: %v", err)
	}
}
