package main

import (
	"encoding/json"
)

type WindowAdd struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type AddWindowTool struct {
	Tool  string `json:"tool"`
	Title string `json:"title"`
	Body  string `json:"body"`
	X     *int   `json:"x,omitempty"`
	Y     *int   `json:"y,omitempty"`
	W     *int   `json:"w,omitempty"`
	H     *int   `json:"h,omitempty"`
}

type AddPluginWindowTool struct {
	Tool       string `json:"tool"`
	WindowType string `json:"window_type"`
	X          *int   `json:"x,omitempty"`
	Y          *int   `json:"y,omitempty"`
	W          *int   `json:"w,omitempty"`
	H          *int   `json:"h,omitempty"`
}

type DelWindowTool struct {
	Tool string `json:"tool"`
	ID   int    `json:"id"`
}

type HighlightWindowTool struct {
	Tool string `json:"tool"`
	ID   int    `json:"id"`
}

type ResizeWindowTool struct {
	Tool string `json:"tool"`
	ID   int    `json:"id"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
}

type InvokeWorkerTool struct {
	Tool string `json:"tool"`
}

type SleepTool struct {
	Tool string `json:"tool"`
}

// PluginToolCall preserves arbitrary plugin arguments and the flattened frontend format.
type PluginToolCall struct {
	Tool      string
	Arguments json.RawMessage
}

func (t PluginToolCall) MarshalJSON() ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(t.Arguments, &fields); err != nil {
		return nil, err
	}
	fields["tool"], _ = json.Marshal(t.Tool)
	return json.Marshal(fields)
}

type ToolWrapper struct {
	Value any // will hold one of the tools
}

type ModelResponse struct {
	Speech string        `json:"speech"`
	Tools  []ToolWrapper `json:"tools"`
}

// extracts tools with varying formats
func (t *ToolWrapper) UnmarshalJSON(data []byte) error {
	var header struct {
		Tool string `json:"tool"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}

	switch header.Tool {
	case "add_window":
		var tool AddWindowTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool
	
	case "add_plugin_window":
		var tool AddPluginWindowTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool

	case "invoke_worker":
		var tool InvokeWorkerTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool

	case "sleep":
		var tool SleepTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool

	case "del_window":
		var tool DelWindowTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool

	case "highlight_window":
		var tool HighlightWindowTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool

	case "resize_window":
		var tool ResizeWindowTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return err
		}
		t.Value = tool

	default:
		if plugins.HasFrontTool(header.Tool) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				return err
			}
			delete(fields, "tool")
			arguments, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			t.Value = PluginToolCall{Tool: header.Tool, Arguments: arguments}
			return nil
		}
		// Optional fallback for unknown/future tools so it doesn't crash
		var generic map[string]any
		if err := json.Unmarshal(data, &generic); err != nil {
			return err
		}
		t.Value = generic
	}

	return nil
}

// without below we {"speech":"Trying it again now.","tools":[{"Value":{"tool":"sleep"}}]}
// instead of {"speech":"Trying it again now.","tools":[{"tool":"sleep"}]}
func (t ToolWrapper) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Value)
}

func parseModelResponse(content string) (ModelResponse, error) {
	jsonData := []byte(content)

	var resp ModelResponse
	err := json.Unmarshal(jsonData, &resp)
	if err != nil {
		return resp, err
	}

	return resp, nil
}
