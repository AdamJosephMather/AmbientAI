package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Reasoning string `json:"reasoning,omitempty"`
	// Keep provider-specific signatures and encrypted reasoning intact for replay.
	ReasoningDetails json.RawMessage `json:"reasoning_details,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Annotations      json.RawMessage `json:"annotations,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type WebPlugin struct {
	ID         string `json:"id"`
	MaxResults int    `json:"max_results,omitempty"`
}

type CompletionOptions struct {
	ResponseFormat json.RawMessage
	Tools          []Tool
	ToolChoice     json.RawMessage
	Plugins        []WebPlugin
	MaxTokens      int
}

type ChatConfig struct {
	APIKey        string  `toml:"api_key"`
	URL           string  `toml:"url"`
	Temperature   float64 `toml:"temperature"`
	MaxTokens     int     `toml:"max_tokens"`
	ToolMaxTokens int     `toml:"tool_max_tokens"`
}

type reasoningParam struct {
	Effort  string `json:"effort"`
	Exclude bool   `json:"exclude,omitempty"`
}

type usageParam struct {
	Include bool `json:"include"`
}

type chatRequest struct {
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Reasoning      *reasoningParam `json:"reasoning,omitempty"`
	Tools          []Tool          `json:"tools,omitempty"`
	ToolChoice     json.RawMessage `json:"tool_choice,omitempty"`
	Plugins        []WebPlugin     `json:"plugins,omitempty"`
	Usage          *usageParam     `json:"usage,omitempty"`
}

type Usage struct {
	Seconds          float64 `json:"seconds,omitempty"`
	InputTokens      int     `json:"input_tokens,omitempty"`
	OutputTokens     int     `json:"output_tokens,omitempty"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	Cost             float64 `json:"cost"` // USD credits charged
	CostDetails      *struct {
		UpstreamInferenceCost float64 `json:"upstream_inference_cost"`
	} `json:"cost_details,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
	Usage *Usage `json:"usage,omitempty"`
}

type Chat struct {
	Config ChatConfig
	Client *http.Client
}

// ToolTokenBudget separates research and reasoning from short spoken replies.
func (c *Chat) ToolTokenBudget() int {
	if c.Config.ToolMaxTokens > 0 {
		return c.Config.ToolMaxTokens
	}
	if c.Config.MaxTokens > 4096 {
		return c.Config.MaxTokens
	}
	return 4096
}

// Complete returns only visible reply text. Call CompleteMessage when maintaining
// a tool conversation so reasoning and tool calls can be saved in history.
func (c *Chat) Complete(ctx context.Context, history []Message, model string, reasoning string) (string, Usage, error) {
	message, usage, err := c.CompleteMessage(ctx, history, model, reasoning, CompletionOptions{})
	return message.Content, usage, err
}

// CompleteMessage returns the assistant message, including reasoning for replay.
// Only Content should be displayed or passed to speech synthesis.
func (c *Chat) CompleteMessage(ctx context.Context, history []Message, model string, reasoning string, options CompletionOptions) (Message, Usage, error) {
	if len(history) == 0 {
		return Message{}, Usage{}, errors.New("empty message history")
	}

	if reasoning != "" && reasoning != "max" && reasoning != "xhigh" && reasoning != "high" && reasoning != "medium" && reasoning != "low" && reasoning != "minimal" && reasoning != "none" {
		return Message{}, Usage{}, errors.New("reasoning level not valid: expected empty, max, xhigh, high, medium, low, minimal, or none")
	}

	messages := make([]Message, 0, len(history)+1)

	messages = append(messages, history...)

	reqData := chatRequest{
		ResponseFormat: options.ResponseFormat,
		Model:          model,
		Messages:       messages,
		MaxTokens:      c.Config.MaxTokens,
		Tools:          options.Tools,
		ToolChoice:     options.ToolChoice,
		Plugins:        options.Plugins,
		Usage:          &usageParam{Include: true},
	}
	if options.MaxTokens > 0 {
		reqData.MaxTokens = options.MaxTokens
	}
	if c.Config.Temperature > 0 {
		t := c.Config.Temperature
		reqData.Temperature = &t
	}

	if reasoning != "" {
		reqData.Reasoning = &reasoningParam{Effort: reasoning}
	}

	body, err := json.Marshal(reqData)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("marshal chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.URL, bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return Message{}, Usage{}, fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, Usage{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Message{}, Usage{}, fmt.Errorf("chat failed (%s): %s", resp.Status, string(respBody))
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return Message{}, Usage{}, fmt.Errorf("decode chat response: %w", err)
	}

	// OpenRouter can return HTTP 200 with an error body (e.g. upstream failure).
	if result.Error != nil {
		return Message{}, Usage{}, fmt.Errorf("chat error: %s (code %v)", result.Error.Message, result.Error.Code)
	}
	if len(result.Choices) == 0 {
		return Message{}, Usage{}, errors.New("chat response had no choices")
	}

	if result.Choices[0].FinishReason == "length" {
		return Message{}, Usage{}, fmt.Errorf("chat response exceeded output token limit (max_tokens=%d); increase the token budget rather than rephrasing the query", reqData.MaxTokens)
	}
	
	var usage Usage
	if result.Usage != nil {
		usage = *result.Usage
	}
	
	return result.Choices[0].Message, usage, nil
}
