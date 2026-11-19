// A standalone test-only plugin. Stdout is exclusively newline-delimited JSON-RPC;
// diagnostics belong on stderr. This plugin performs no external actions.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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

func main() {
	if err := serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve() error {
	encoder := json.NewEncoder(os.Stdout)
	var outputMu sync.Mutex
	send := func(value message) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return encoder.Encode(value)
	}
	registration := json.RawMessage(`{
		"name":"Test", "version":"1.0.0",
		"front_end_tools":[
			{"name":"echo","description":"Return the supplied test text without performing any action.","parameters":{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}}
		],
		"back_end_tools":[
			{"name":"add","description":"Add two test numbers.","parameters":{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"],"additionalProperties":false}},
			{"name":"wait","description":"Wait between 0 and 120 seconds to test plugin timeouts.","parameters":{"type":"object","properties":{"seconds":{"type":"number","minimum":0,"maximum":120}},"required":["seconds"],"additionalProperties":false}},
			{"name":"fail","description":"Return an intentional test error.","parameters":{"type":"object","properties":{},"additionalProperties":false}}
		]
	}`)
	if err := send(message{JSONRPC: "2.0", ID: json.RawMessage(`"register"`), Method: "plugin/register", Params: registration}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	if !scanner.Scan() {
		return fmt.Errorf("registration acknowledgement missing: %v", scanner.Err())
	}
	var ack message
	if err := json.Unmarshal(scanner.Bytes(), &ack); err != nil {
		return err
	}
	if ack.JSONRPC != "2.0" || string(ack.ID) != `"register"` || ack.Error != nil || len(ack.Result) == 0 {
		return fmt.Errorf("registration rejected")
	}
	// Opt in to independent timer events to demonstrate notifications while idle
	// and during tools/call. Real plugins can send these from their event listeners.
	stopUpdates := make(chan struct{})
	defer close(stopUpdates)
	if intervalText := os.Getenv("EXAMPLE_PLUGIN_UPDATE_INTERVAL"); intervalText != "" {
		interval, err := time.ParseDuration(intervalText)
		if err != nil || interval < 10*time.Millisecond {
			return fmt.Errorf("EXAMPLE_PLUGIN_UPDATE_INTERVAL must be a duration of at least 10ms")
		}
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for sequence := 1; ; sequence++ {
				select {
				case <-stopUpdates:
					return
				case <-ticker.C:
					params, _ := json.Marshal(map[string]any{"message": "Example plugin timer event", "data": map[string]int{"sequence": sequence}})
					if err := send(message{JSONRPC: "2.0", Method: "plugin/notify", Params: params}); err != nil {
						fmt.Fprintln(os.Stderr, err)
						return
					}
				}
			}
		}()
	}
	for scanner.Scan() {
		var request message
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		response := message{JSONRPC: "2.0", ID: request.ID}
		var params struct {
			Name      string                     `json:"name"`
			Target    string                     `json:"target"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if request.JSONRPC != "2.0" || request.Method != "tools/call" {
			response.Error = &rpcError{-32601, "Unknown method"}
		} else if err := json.Unmarshal(request.Params, &params); err != nil || params.Arguments == nil {
			response.Error = &rpcError{-32602, "Invalid tool parameters"}
		} else {
			var result any
			switch {
			case params.Target == "front_end" && params.Name == "echo":
				var text string
				if err := json.Unmarshal(params.Arguments["text"], &text); err != nil {
					response.Error = &rpcError{-32602, "text must be a string"}
				} else {
					result = map[string]string{"text": text}
				}
			case params.Target == "back_end" && params.Name == "add":
				var a, b float64
				if json.Unmarshal(params.Arguments["a"], &a) != nil || json.Unmarshal(params.Arguments["b"], &b) != nil {
					response.Error = &rpcError{-32602, "a and b must be numbers"}
				} else {
					result = map[string]float64{"sum": a + b}
				}
			case params.Target == "back_end" && params.Name == "wait":
				var seconds float64
				if json.Unmarshal(params.Arguments["seconds"], &seconds) != nil || seconds < 0 || seconds > 120 {
					response.Error = &rpcError{-32602, "seconds must be between 0 and 120"}
				} else {
					time.Sleep(time.Duration(seconds * float64(time.Second)))
					result = map[string]bool{"finished": true}
				}
			case params.Target == "back_end" && params.Name == "fail":
				response.Error = &rpcError{-32000, "Intentional test failure"}
			default:
				response.Error = &rpcError{-32601, "Unknown tool or target"}
			}
			if response.Error == nil {
				response.Result, _ = json.Marshal(result)
			}
		}
		if err := send(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}
