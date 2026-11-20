package main

import (
	"AmbientAI/openrouter"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const pluginRegistrationTimeout = 5 * time.Second
const maxPluginMessage = 1024 * 1024

type PluginConfig struct {
	Paths              []string `toml:"paths"`
	CallTimeoutSeconds int      `toml:"call_timeout_seconds"`
}

// PluginUpdate is an unsolicited event from a registered plugin. The loader
// supplies Plugin so a notification cannot impersonate another plugin.
type PluginUpdate struct {
	Plugin  string
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type PluginWindowUpdate struct {
	Plugin   string
	WindowID *int   `json:"window_id"`
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
}

type pluginTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type pluginWindowType struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
}

type pluginRegistration struct {
	Name          string             `json:"name"`
	Version       string             `json:"version"`
	FrontEndTools []pluginTool       `json:"front_end_tools"`
	BackEndTools  []pluginTool       `json:"back_end_tools"`
	WindowTypes   []pluginWindowType `json:"window_types,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type pluginMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// One reader owns stdout. Calls on each process are serialized; separate
// processes can run concurrently. Close interrupts both reads and writes.
type pluginProcess struct {
	cmd           *exec.Cmd
	in            io.WriteCloser
	out           io.ReadCloser
	messages      chan pluginMessage
	registered    chan struct{}
	readerDone    chan struct{}
	name          string // published by closing registered
	updates       chan<- PluginUpdate
	windowupdates chan<- PluginWindowUpdate
	ownedWindows  map[int]bool
	owndWindowsMu sync.Mutex
	done          chan struct{}
	gate          chan struct{}
	writeMutex    sync.Mutex
	closeOnce     sync.Once
	failOnce      sync.Once
	err           error  // published by closing done
	nextID        uint64 // owned by gate
}

func (p *pluginProcess) fail(err error) {
	p.failOnce.Do(func() { p.err = err; close(p.done) })
}

func (p *pluginProcess) close() {
	p.closeOnce.Do(func() {
		p.fail(errors.New("plugin stopped"))
		_ = p.cmd.Process.Kill()
		_ = p.in.Close()
		_ = p.out.Close()
	})
}

func startPlugin(path string) (*pluginProcess, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(absolute)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, err
	}
	p := &pluginProcess{cmd: cmd, in: in, out: out, messages: make(chan pluginMessage), done: make(chan struct{}), gate: make(chan struct{}, 1), ownedWindows: make(map[int]bool), registered: make(chan struct{}), readerDone: make(chan struct{})}
	go func() {
		defer close(p.readerDone)
		registered := false
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), maxPluginMessage)
		for scanner.Scan() {
			var message pluginMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				p.fail(fmt.Errorf("invalid plugin JSON: %w", err))
				return
			}
			if message.JSONRPC != "2.0" {
				p.fail(errors.New("expected JSON-RPC 2.0"))
				return
			}
			if registered && message.Method != "" {
				var err error
				switch message.Method {
				case "plugin/notify":
					err = p.notify(message)
				case "plugin/updatewindow":
					err = p.updateWindow(message)
				default:
					err = fmt.Errorf("unknown method type %s", message.Method)
				}
				if err != nil {
					p.fail(err)
					return
				}
				continue
			}
			select {
			case p.messages <- message:
			case <-p.done:
				return
			}
			if !registered {
				select {
				case <-p.registered:
					registered = true
				case <-p.done:
					return
				}
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		p.fail(fmt.Errorf("plugin stdout closed: %w", err))
	}()
	// Wait only after the reader has drained stdout (StdoutPipe's contract).
	go func() { <-p.done; p.close(); _ = cmd.Wait() }()
	return p, nil
}

func (p *pluginProcess) notify(message pluginMessage) error {
	if message.Method != "plugin/notify" || len(message.ID) != 0 || len(message.Result) != 0 || message.Error != nil {
		return errors.New("expected a plugin/notify notification without an id")
	}
	var update PluginUpdate
	if err := json.Unmarshal(message.Params, &update); err != nil || strings.TrimSpace(update.Message) == "" {
		return errors.New("plugin/notify params need a nonempty message string")
	}
	update.Plugin = p.name
	select {
	case p.updates <- update:
	case <-p.done:
	default:
		// Never let a burst of events block tool replies on the stdout reader.
		fmt.Fprintf(os.Stderr, "Plugin %s update dropped: notification queue is full\n", p.name)
	}
	return nil
}

func (p *pluginProcess) updateWindow(message pluginMessage) error {
	if message.Method != "plugin/updatewindow" || len(message.ID) != 0 || len(message.Result) != 0 || message.Error != nil {
		return errors.New("expected a plugin/updatewindow notification without an id")
	}
	var update PluginWindowUpdate
	if err := json.Unmarshal(message.Params, &update); err != nil {
		return errors.New("plugin/updatewindow couldn't decode")
	}
	
	if update.WindowID == nil {
		return errors.New("plugin/updatewindow missing ID")
	}
	
	p.owndWindowsMu.Lock()
	defer p.owndWindowsMu.Unlock()
	
	if !p.ownedWindows[*update.WindowID] {
		// we'll return nil in case the plugin sent this update before the window was closed. Don't kill the plugin over this
		return nil
	}
	
	update.Plugin = p.name
	select {
	case p.windowupdates <- update:
	case <-p.done:
	default:
		// Never let a burst of events block tool replies on the stdout reader.
		fmt.Fprintf(os.Stderr, "Plugin %s window update dropped: update queue is full\n", p.name)
	}
	return nil
}

func (p *pluginProcess) write(ctx context.Context, message pluginMessage) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	
	written := make(chan error, 1)
	go func() {
		p.writeMutex.Lock()
		defer p.writeMutex.Unlock()
		_, err := p.in.Write(append(data, '\n'))
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			p.close()
		}
		return err
	case <-ctx.Done():
		p.close()
		return ctx.Err()
	case <-p.done:
		return p.err
	}
}

type registeredPluginTool struct {
	process    *pluginProcess
	definition pluginTool
}

type registeredPluginWindow struct {
	process    *pluginProcess
	definition pluginWindowType
}

// PluginManager is immutable after LoadPlugins, apart from process state.
type PluginManager struct {
	front         map[string]registeredPluginTool
	back          map[string]registeredPluginTool
	windowtypes   map[string]registeredPluginWindow
	processes     []*pluginProcess
	timeout       time.Duration
	workerTools   []openrouter.Tool
	updates       chan PluginUpdate
	windowupdates chan PluginWindowUpdate
	closeOnce     sync.Once
}

var pluginNamePart = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
var validToolName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func pluginNamespace(name string) string {
	return strings.Trim(pluginNamePart.ReplaceAllString(name, "_"), "_")
}

func validatePluginTool(tool pluginTool, front bool) error {
	if !validToolName.MatchString(tool.Name) || strings.TrimSpace(tool.Description) == "" {
		return fmt.Errorf("tool %q needs a valid name and description", tool.Name)
	}
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(tool.Parameters, &schema); err != nil || schema.Type != "object" {
		return fmt.Errorf("tool %q parameters must be an object JSON Schema", tool.Name)
	}
	for _, key := range schema.Required {
		if _, ok := schema.Properties[key]; !ok {
			return fmt.Errorf("tool %q requires undefined property %q", tool.Name, key)
		}
	}
	if _, ok := schema.Properties["tool"]; front && ok {
		return fmt.Errorf("tool %q uses reserved frontend parameter 'tool'", tool.Name)
	}
	return nil
}

func validateWindowType(window pluginWindowType) error {
	if !validToolName.MatchString(window.Name) || strings.TrimSpace(window.Description) == "" {
		return fmt.Errorf("window %q needs a valid name and description", window.Name)
	}
	return nil
}

// LoadPlugins accepts only fully valid registrations. A failed plugin never
// contributes partial tools. Relative executable paths use the assistant's working dir.
func LoadPlugins(config *PluginConfig) (*PluginManager, string, string, string) {
	m := &PluginManager{front: make(map[string]registeredPluginTool), back: make(map[string]registeredPluginTool), windowtypes: make(map[string]registeredPluginWindow), timeout: 30 * time.Second, updates: make(chan PluginUpdate, 128), windowupdates: make(chan PluginWindowUpdate, 128)}
	if config == nil {
		return m, "", "", ""
	}
	if config.CallTimeoutSeconds > 0 {
		m.timeout = time.Duration(config.CallTimeoutSeconds) * time.Second
	}
	var failures, windows, prompt []string
	names := make(map[string]bool)
	usedTools := map[string]bool{"add_window": true, "add_plugin_window": true,  "del_window": true, "highlight_window": true, "resize_window": true, "invoke_worker": true, "sleep": true, "inject_context": true}
	usedWindows := make(map[string]bool)
	
	for _, path := range config.Paths {
		p, err := startPlugin(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), pluginRegistrationTimeout)
		registration, err := m.register(ctx, p, names, usedTools, usedWindows)
		cancel()
		if err != nil {
			p.close()
			failures = append(failures, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		m.processes = append(m.processes, p)
		names[strings.ToLower(pluginNamespace(registration.Name))] = true
		for _, tool := range registration.FrontEndTools {
			name := pluginNamespace(registration.Name) + "_" + tool.Name
			m.front[name] = registeredPluginTool{p, tool}
			usedTools[strings.ToLower(name)] = true
			// Flatten only the call example; retain the complete schema so required,
			// optional, and cross-field constraints remain available to the model.
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			_ = json.Unmarshal(tool.Parameters, &schema)
			example := map[string]any{"tool": name}
			for key := range schema.Properties {
				example[key] = "<value>"
			}
			encoded, _ := json.Marshal(example)
			prompt = append(prompt, fmt.Sprintf("%s\n- %s\nParameter JSON Schema: %s", encoded, tool.Description, tool.Parameters))
		}
		for _, tool := range registration.BackEndTools {
			name := pluginNamespace(registration.Name) + "_" + tool.Name
			m.back[name] = registeredPluginTool{p, tool}
			usedTools[strings.ToLower(name)] = true
			m.workerTools = append(m.workerTools, openrouter.Tool{Type: "function", Function: openrouter.FunctionDefinition{Name: name, Description: tool.Description, Parameters: tool.Parameters}})
		}
		for _, window := range registration.WindowTypes {
			name := pluginNamespace(registration.Name) + "_" + window.Name
			m.windowtypes[name] = registeredPluginWindow{p, window}
			usedWindows[strings.ToLower(name)] = true
			windows = append(windows, fmt.Sprintf("%s : %s", name, window.Description))
		}
	}
	loadErrors := ""
	if len(failures) > 0 {
		loadErrors = "Plugin load errors. Inform the user which plugins could not load and why; do not claim their tools are available.\n" + strings.Join(failures, "\n")
	}
	return m, strings.Join(prompt, "\n\n"), strings.Join(windows, "\n\n"), loadErrors
}

func (m *PluginManager) register(ctx context.Context, p *pluginProcess, names, used map[string]bool, usedWindows map[string]bool) (pluginRegistration, error) {
	var reg pluginRegistration
	var request pluginMessage
	select {
	case request = <-p.messages:
	case <-ctx.Done():
		return reg, fmt.Errorf("registration timed out after 5 seconds: %w", ctx.Err())
	case <-p.done:
		return reg, p.err
	}
	var id any
	if json.Unmarshal(request.ID, &id) != nil || id == nil || request.Method != "plugin/register" || len(request.Result) != 0 || request.Error != nil {
		return reg, errors.New("expected plugin/register JSON-RPC request with an id")
	}
	switch id.(type) {
	case string, float64:
	default:
		return reg, errors.New("registration id must be a string or number")
	}
	if err := json.Unmarshal(request.Params, &reg); err != nil {
		return reg, err
	}
	namespace := pluginNamespace(reg.Name)
	if namespace == "" || strings.TrimSpace(reg.Version) == "" {
		return reg, errors.New("registration needs a name and version")
	}
	if names[strings.ToLower(namespace)] {
		return reg, fmt.Errorf("duplicate plugin namespace %q", namespace)
	}
	// Each side has its own tool registry, so the same plugin tool can be
	// intentionally exposed on both sides under the same namespaced name.
	for i, tools := range [][]pluginTool{reg.FrontEndTools, reg.BackEndTools} {
		local := make(map[string]bool)
		for _, tool := range tools {
			if err := validatePluginTool(tool, i == 0); err != nil {
				return reg, err
			}
			name := namespace + "_" + tool.Name
			key := strings.ToLower(name)
			if len(name) > 64 || used[key] || local[key] {
				return reg, fmt.Errorf("duplicate or oversized tool name %q (maximum 64 characters)", name)
			}
			local[key] = true
		}
	}
	
	local := make(map[string]bool)
	for _, window := range reg.WindowTypes {
		if err := validateWindowType(window); err != nil {
			return reg, err
		}
		name := namespace + "_" + window.Name
		key := strings.ToLower(name)
		if len(name) > 64 || usedWindows[key] || local[key] {
			return reg, fmt.Errorf("duplicate or oversized window type name %q (maximum 64 characters)", name)
		}
		local[key] = true
	}
	
	if err := p.write(ctx, pluginMessage{JSONRPC: "2.0", ID: request.ID, Result: json.RawMessage(`{"registered":true}`)}); err != nil {
		return reg, err
	}
	p.name, p.updates, p.windowupdates = reg.Name, m.updates, m.windowupdates
	close(p.registered)
	return reg, nil
}

func (m *PluginManager) Close() {
	if m != nil {
		m.closeOnce.Do(func() {
			for _, p := range m.processes {
				p.close()
			}
			for _, p := range m.processes {
				<-p.readerDone
			}
			close(m.updates)
			close(m.windowupdates)
		})
	}
}

// Updates is drained by the main conversation loop after initialization. It
// closes on Close; events may arrive while idle or while a tool call is running.
func (m *PluginManager) Updates() <-chan PluginUpdate { return m.updates }
func (m *PluginManager) WindowUpdates() <-chan PluginWindowUpdate { return m.windowupdates }
func (m *PluginManager) HasFrontTool(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m.front[name]
	return ok
}

func (m *PluginManager) HasWindowType(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m.windowtypes[name]
	return ok
}

func (m *PluginManager) WorkerTools() []openrouter.Tool {
	if m == nil {
		return nil
	}
	return append([]openrouter.Tool(nil), m.workerTools...)
}

func (m *PluginManager) Call(ctx context.Context, target, name string, arguments json.RawMessage) (string, error) {
	if m == nil {
		return "", errors.New("plugins unavailable")
	}
	registry := m.front
	if target == "back_end" {
		registry = m.back
	} else if target != "front_end" {
		return "", errors.New("invalid plugin target")
	}
	tool, ok := registry[name]
	if !ok {
		return "", fmt.Errorf("unknown plugin tool %q", name)
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(arguments, &args) != nil || args == nil {
		return "", errors.New("plugin arguments must be a JSON object")
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	p := tool.process
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return "", fmt.Errorf("plugin tool %s failed waiting to run: %w", name, ctx.Err())
	case <-p.done:
		return "", p.err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case <-p.done:
		return "", p.err
	default:
	}
	p.nextID++
	id := json.RawMessage(fmt.Sprint(p.nextID))
	params, _ := json.Marshal(struct {
		Name      string          `json:"name"`
		Target    string          `json:"target"`
		Arguments json.RawMessage `json:"arguments"`
	}{tool.definition.Name, target, arguments})
	if err := p.write(ctx, pluginMessage{JSONRPC: "2.0", ID: id, Method: "tools/call", Params: params}); err != nil {
		return "", fmt.Errorf("plugin tool %s failed: %w", name, err)
	}
	for {
		select {
		case response := <-p.messages:
			if string(response.ID) != string(id) || response.Method != "" {
				p.close()
				return "", errors.New("unexpected plugin response id or method")
			}
			if response.Error != nil && len(response.Result) == 0 {
				return "", fmt.Errorf("plugin tool %s error %d: %s (data: %s)", name, response.Error.Code, response.Error.Message, response.Error.Data)
			}
			if response.Error != nil || len(response.Result) == 0 {
				p.close()
				return "", errors.New("plugin response must contain exactly one of result or error")
			}
			return string(response.Result), nil
		case <-ctx.Done():
			p.close()
			return "", fmt.Errorf("plugin tool %s failed; plugin stopped: %w", name, ctx.Err())
		case <-p.done:
			return "", fmt.Errorf("plugin tool %s failed: %w", name, p.err)
		}
	}
}

func (m *PluginManager) InformOfWindow(ctx context.Context, winid int, wintype string, deleted bool) error {
	if m == nil {
		return errors.New("plugins unavailable")
	}
	
	window, ok := m.windowtypes[wintype]
	if !ok {
		return fmt.Errorf("unknown plugin window %q", wintype)
	}
	
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	p := window.process
	
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return p.err
	default:
	}
	
	params, _ := json.Marshal(struct {
		Id        int             `json:"window_id"`
		WinType   string          `json:"window_type"`
	}{winid, window.definition.Name})
	
	p.owndWindowsMu.Lock()
	method := "windows/created"
	if deleted {
		method = "windows/deleted"
		delete(p.ownedWindows, winid)
	}else{
		p.ownedWindows[winid] = true
	}
	p.owndWindowsMu.Unlock()
	
	if err := p.write(ctx, pluginMessage{JSONRPC: "2.0", Method: method, Params: params}); err != nil {
		return fmt.Errorf("plugin window %s failed: %w", wintype, err)
	}
	// we don't expect a response
	return nil
}