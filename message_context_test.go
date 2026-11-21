package main

import (
	"AmbientAI/openrouter"
	"strings"
	"testing"
	"time"
)

func TestMessageContext(t *testing.T) {
	at := time.Date(2026, 11, 20, 9, 7, 0, 0, time.FixedZone("MST", -7*3600))
	got := timestampMessage("[From: Plugin: Spotify]\n\nSong changed", "user", at)
	if got != "[From: Plugin: Spotify | 09:07]\n\nSong changed" {
		t.Fatal(got)
	}
	prompt := datedSystemPrompt("Instructions", at)
	if !strings.HasPrefix(prompt, "Instructions"+dateContextPrefix) || !strings.Contains(prompt, "Friday, 2026-11-20") || !strings.Contains(prompt, "UTC-07:00") {
		t.Fatal(prompt)
	}
	// Requests later the same day must preserve the cacheable system prefix.
	if later := datedSystemPrompt(prompt, at.Add(time.Hour)); later != prompt {
		t.Fatalf("system prompt changed during the day: %s", later)
	}
	history := []openrouter.Message{{Role: "system", Content: prompt}, {Role: "user", Content: got}}
	refreshSystemDates(history, at.AddDate(0, 0, 1))
	if !strings.Contains(history[0].Content, "Saturday, 2026-11-21") || strings.Count(history[0].Content, dateContextPrefix) != 1 || history[1].Content != got {
		t.Fatalf("date rollover changed history incorrectly: %+v", history)
	}
}
