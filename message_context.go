package main

import (
	"AmbientAI/openrouter"
	"fmt"
	"strings"
	"time"
)

func formatMessage(sender, message string, at time.Time) string {
	return fmt.Sprintf("[From: %s | %s]\n\n%s", sender, at.Format("15:04"), message)
}

// parseFromTag extracts the sender and body from an existing
// "[From: sender | HH:mm]\n\n" envelope. It returns an empty sender and the
// original message when there is no valid envelope.
func parseFromTag(message string) (sender, body string) {
	if !strings.HasPrefix(message, "[From: ") {
		return "", message
	}
	end := strings.Index(message, "]\n\n")
	if end < 0 {
		return "", message
	}
	sender = message[len("[From: "):end]
	// A stored envelope may carry a stale timestamp suffix; drop it.
	if idx := strings.LastIndex(sender, " | "); idx >= 0 {
		sender = sender[:idx]
	}
	return sender, message[end+3:]
}

// Existing event producers supply their sender in an envelope.
func timestampMessage(message, sender string, at time.Time) string {
	if parsed, body := parseFromTag(message); parsed != "" {
		sender, message = parsed, body
	}
	return formatMessage(sender, message, at)
}

// Keep changing date metadata at the end to preserve the static prompt prefix.
const dateContextPrefix = "\n\nCurrent local date: "

func datedSystemPrompt(prompt string, at time.Time) string {
	if start := strings.LastIndex(prompt, dateContextPrefix); start >= 0 {
		prompt = prompt[:start]
	}

	zone, _ := at.Zone()
	return fmt.Sprintf("%s%s%s (timezone: %s, UTC%s). Message timestamps use this local timezone.",
		prompt, dateContextPrefix, at.Format("Monday, 2006-01-02"), zone, at.Format("-07:00"))
}

func refreshSystemDates(history []openrouter.Message, at time.Time) {
	for i := range history {
		if history[i].Role == "system" && strings.Contains(history[i].Content, dateContextPrefix) {
			history[i].Content = datedSystemPrompt(history[i].Content, at)
		}
	}
}

func refreshedSystemDates(history []openrouter.Message) []openrouter.Message {
	copy := append([]openrouter.Message(nil), history...)
	refreshSystemDates(copy, time.Now())
	return copy
}
