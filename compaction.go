package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"AmbientAI/openrouter"
)

// latestCompaction returns the most recent compaction event (or nil) and the
// message events that follow it. Everything at or before the compaction has
// been folded into it.
func latestCompaction(events []Event) (*Event, []Event) {
	compaction := -1
	for i := range events {
		if events[i].Type == EventCompaction {
			compaction = i
		}
	}
	start := 0
	if compaction >= 0 {
		start = compaction + 1
	}
	tail := make([]Event, 0)
	for i := start; i < len(events); i++ {
		if events[i].Type == EventMessage {
			tail = append(tail, events[i])
		}
	}
	if compaction < 0 {
		return nil, tail
	}
	c := events[compaction]
	return &c, tail
}

// lastMessageEvent returns the most recent message event in events, or nil.
func lastMessageEvent(events []Event) *Event {
	var last *Event
	for i := range events {
		if events[i].Type == EventMessage {
			c := events[i]
			last = &c
		}
	}
	return last
}

// shouldCompact reports whether the last message was sent more than
// afterHours hours ago.
func shouldCompact(last *Event, now time.Time, afterHours int) bool {
	if last == nil || afterHours <= 0 {
		return false
	}
	return now.Unix()-last.Time.Unix() > int64(afterHours)*3600
}

// compactionPrompt builds the compaction model's input. Assistant (Zoe's)
// turns are wrapped in a [From: aiName | HH:mm] envelope, like every other
// non-user turn. aiName is the name used in the envelope.
func compactionPrompt(priorCompaction string, tail []Event, aiName string) string {
	var b strings.Builder
	b.WriteString("You are the compactor of an ambient assistant's conversation history.\n\n")
	b.WriteString("Summarize everything below into a single compact summary. The summary replaces the messages it was derived from in the assistant's context, so anything the assistant needs to continue the conversation must survive it.\n\n")

	if strings.TrimSpace(priorCompaction) != "" {
		b.WriteString("Previous summary:\n")
		b.WriteString(priorCompaction)
		b.WriteString("\n\n")
	}

	b.WriteString("Messages to fold in:\n")
	for _, ev := range tail {
		content := ev.Content
		if ev.Role == "assistant" {
			_, body := parseFromTag(ev.Content)
			content = formatMessage(aiName, body, ev.Time.Local())
		}
		fmt.Fprintf(&b, "---\n[%s] %s\n", ev.Time.Local().Format("2006-01-02 15:04"), content)
	}

	b.WriteString("\nRules:\n")
	b.WriteString("- Preserve the user's goals, ongoing tasks, pending asks, and expectations.\n")
	b.WriteString("- Preserve important facts, decisions, preferences, results, and anything the assistant promised or found that still matters.\n")
	b.WriteString("- Greetings, small talk, routine state chatter, and redundant details may be dropped.\n")
	b.WriteString("- No dialogue formatting; write an objective account of what happened.\n")
	b.WriteString("- Do not invent information that is not present.\n")
	b.WriteString("- Output only the summary: no preamble, no labels, no Markdown fences. Aim for under ~200 words.")

	return b.String()
}

// compactHistory summarizes the prior compaction together with the messages
// after it into a single lossy summary. It uses the configured smart model.
func compactHistory(ctx context.Context, priorCompaction string, tail []Event, maxCompactTokens int, aiName string) (string, error) {
	msg, _, err := chat.CompleteMessage(ctx, []openrouter.Message{
		{Role: "user", Content: compactionPrompt(priorCompaction, tail, aiName)},
	}, aiConfig.ModelSmart, aiConfig.ModelSmartReasoning, openrouter.CompletionOptions{MaxTokens: maxCompactTokens})
	if err != nil {
		return "", fmt.Errorf("compact history: %w", err)
	}
	return strings.TrimSpace(msg.Content), nil
}

// compactionMessageContent wraps a compact summary the same way the
// assistant presents a message in history.
func compactionMessageContent(content string, at time.Time) string {
	return fmt.Sprintf("[Compaction | %s]\n\n%s", at.Local().Format("15:04"), content)
}

// prepareHistory restores the conversation after a restart: it reads all
// events from the database, compacts the history if the last message is
// stale, and returns the resulting assistant and worker context. aiName is
// the name used in the [From: aiName | HH:mm] envelope on the worker's copy
// of assistant messages.
func prepareHistory(now time.Time, compactAfterHours, maxCompactTokens int, aiName string) ([]openrouter.Message, []string, error) {
	events, err := eventDB.Events()
	if err != nil {
		return nil, nil, fmt.Errorf("read conversation history: %w", err)
	}

	compaction, tail := latestCompaction(events)
	lastMessage := lastMessageEvent(events)

	if lastMessage != nil && shouldCompact(lastMessage, now, compactAfterHours) {
		prior := ""
		if compaction != nil {
			prior = compaction.Content
		}
		
		println("Compacting...")
		
		summary, err := compactFn(prior, tail, maxCompactTokens, aiName)
		if err != nil {
			// Fall back to the un-compacted history.
			fmt.Printf("Conversation compaction failed: %v\n", err)
		} else if strings.TrimSpace(summary) == "" {
			fmt.Println("Conversation compaction produced an empty summary; falling back to un-compacted history")
		} else {
			if err := eventDB.InsertCompaction(now, summary); err != nil {
				// Fall back to the un-compacted history.
				fmt.Printf("Could not store compaction in the conversation database: %v\n", err)
			} else {
				compaction = &Event{Time: now, Type: EventCompaction, Role: "user", Content: summary}
				tail = nil
			}
		}
	}

	assistantContext := make([]openrouter.Message, 0, len(tail)+1)
	workerContext := make([]string, 0, len(tail)+1)
	if compaction != nil {
		content := compactionMessageContent(compaction.Content, compaction.Time)
		assistantContext = append(assistantContext, openrouter.Message{Role: "user", Content: content})
		workerContext = append(workerContext, content)
	}
	for _, ev := range tail {
		if ev.Role == "assistant" {
			// Assistant output is JSON and is kept raw for the main model.
			// Only the worker's copy carries the [From: aiName | HH:mm] envelope.
			_, body := parseFromTag(ev.Content)
			assistantContext = append(assistantContext, openrouter.Message{Role: ev.Role, Content: body})
			workerContext = append(workerContext, formatMessage(aiName, body, ev.Time.Local()))
			continue
		}
		assistantContext = append(assistantContext, openrouter.Message{Role: ev.Role, Content: ev.Content})
		workerContext = append(workerContext, ev.Content)
	}
	return assistantContext, workerContext, nil
}

// compactFn is the function used to summarize prior compaction + tail.
// It is a variable so tests can substitute a stub.
var compactFn = func(prior string, tail []Event, maxCompactTokens int, aiName string) (string, error) {
	return compactHistory(context.Background(), prior, tail, maxCompactTokens, aiName)
}
