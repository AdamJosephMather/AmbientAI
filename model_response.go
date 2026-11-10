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
}

type InvokeWorkerTool struct {
	Tool string `json:"tool"`
}

type SleepTool struct {
	Tool string `json:"tool"`
}

type ToolWrapper struct {
	Value any // will hold one of the tools
}

type ModelResponse struct {
	Speech    string        `json:"speech"`
	Tools     []ToolWrapper `json:"tools"`
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

	default:
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