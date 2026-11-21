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

// Existing event producers supply their sender in an envelope.
func timestampMessage(message, sender string, at time.Time) string {
	if strings.HasPrefix(message, "[From: ") {
		if end := strings.Index(message, "]\n\n"); end >= 0 {
			sender, message = message[len("[From: "):end], message[end+3:]
		}
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
