package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func parseModelResponse(content string) (ModelResponse, error) {
	var response ModelResponse
	if !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return response, errors.New("expected a JSON response object")
	}
	if err := json.Unmarshal([]byte(content), &response); err == nil {
		return response, nil
	}

	// Some providers still emit literal newlines in Markdown strings. Escape
	// only unescaped control characters inside strings, preserving JSON layout
	// and existing escapes. Leave unrelated syntax errors for the JSON decoder.
	var repaired strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(content); i++ {
		c := content[i]
		if inString && !escaped && c < 0x20 {
			fmt.Fprintf(&repaired, `\u%04x`, c)
			continue
		}
		repaired.WriteByte(c)
		if escaped {
			escaped = false
		} else if inString && c == '\\' {
			escaped = true
		} else if c == '"' {
			inString = !inString
		}
	}
	response = ModelResponse{}
	if err := json.Unmarshal([]byte(repaired.String()), &response); err != nil {
		return ModelResponse{}, err
	}
	return response, nil
}
