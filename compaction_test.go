package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AmbientAI/openrouter"
)

var errCompactionStub = errors.New("stub compaction failure")

func TestLatestCompactionNoCompaction(t *testing.T) {
	now := time.Now()
	events := []Event{
		{Time: now, Type: EventProgramStart},
		{Time: now, Type: EventMessage, Role: "user", Content: "hello"},
		{Time: now, Type: EventMessage, Role: "assistant", Content: "hi"},
	}

	compaction, tail := latestCompaction(events)
	if compaction != nil {
		t.Errorf("expected nil compaction, got %+v", compaction)
	}
	if len(tail) != 2 {
		t.Fatalf("expected 2 tail messages, got %d", len(tail))
	}
	if tail[0].Content != "hello" || tail[1].Content != "hi" {
		t.Errorf("tail = %+v", tail)
	}
}

func TestLatestCompactionWithCompaction(t *testing.T) {
	now := time.Now()
	events := []Event{
		{Time: now, Type: EventProgramStart},
		{Time: now, Type: EventMessage, Role: "user", Content: "old one"},
		{Time: now, Type: EventMessage, Role: "assistant", Content: "old two"},
		{Time: now, Type: EventCompaction, Role: "system", Content: "old summary"},
		{Time: now, Type: EventMessage, Role: "user", Content: "new one"},
		{Time: now, Type: EventMessage, Role: "assistant", Content: "new two"},
	}

	compaction, tail := latestCompaction(events)
	if compaction == nil {
		t.Fatal("expected a compaction")
	}
	if compaction.Content != "old summary" {
		t.Errorf("compaction content = %q", compaction.Content)
	}
	if len(tail) != 2 || tail[0].Content != "new one" || tail[1].Content != "new two" {
		t.Errorf("tail = %+v", tail)
	}
}

func TestLatestCompactionUsesLastCompaction(t *testing.T) {
	now := time.Now()
	events := []Event{
		{Time: now, Type: EventMessage, Role: "user", Content: "a"},
		{Time: now, Type: EventCompaction, Role: "system", Content: "summary one"},
		{Time: now, Type: EventMessage, Role: "user", Content: "b"},
		{Time: now, Type: EventCompaction, Role: "system", Content: "summary two"},
		{Time: now, Type: EventMessage, Role: "user", Content: "c"},
	}

	compaction, tail := latestCompaction(events)
	if compaction == nil || compaction.Content != "summary two" {
		t.Fatalf("expected latest compaction, got %+v", compaction)
	}
	if len(tail) != 1 || tail[0].Content != "c" {
		t.Errorf("tail = %+v", tail)
	}
}

func TestShouldCompact(t *testing.T) {
	now := time.Now()
	old := now.Add(-4 * time.Hour)
	fresh := now.Add(-1 * time.Hour)

	cases := []struct {
		name       string
		last       *Event
		afterHours int
		want       bool
	}{
		{"no last message", nil, 3, false},
		{"stale message", &Event{Time: old}, 3, true},
		{"fresh message", &Event{Time: fresh}, 3, false},
		{"zero threshold", &Event{Time: old}, 0, false},
		{"negative threshold", &Event{Time: old}, -1, false},
		{"borderline age", &Event{Time: now.Add(-3*time.Hour - time.Second)}, 3, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldCompact(tc.last, now, tc.afterHours); got != tc.want {
				t.Errorf("shouldCompact() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompactionMessageContent(t *testing.T) {
	at := time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)
	content := compactionMessageContent("summary text", at)
	if !strings.Contains(content, "[Compaction |") {
		t.Errorf("missing header: %q", content)
	}
	if !strings.Contains(content, "summary text") {
		t.Errorf("missing body: %q", content)
	}
}

func TestPrepareHistoryFreshConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenConversationDB(path)
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	defer db.Close()
	eventDB = db
	t.Cleanup(func() { eventDB = nil })

	at := time.Now()
	must(t, db.InsertProgramStart(at))
	must(t, db.InsertMessage(at.Add(time.Minute), "user", "hello"))
	must(t, db.InsertMessage(at.Add(2*time.Minute), "assistant", "hi there"))

	assistantContext, workerContext, err := prepareHistory(at.Add(3*time.Minute), 3)
	if err != nil {
		t.Fatalf("prepareHistory: %v", err)
	}

	// No compaction should have happened.
	if len(assistantContext) != 2 {
		t.Fatalf("expected 2 context messages, got %d: %+v", len(assistantContext), assistantContext)
	}
	if assistantContext[0].Role != "user" || assistantContext[0].Content != "hello" {
		t.Errorf("context[0] = %+v", assistantContext[0])
	}
	if assistantContext[1].Role != "assistant" || assistantContext[1].Content != "hi there" {
		t.Errorf("context[1] = %+v", assistantContext[1])
	}
	if len(workerContext) != 2 || workerContext[0] != "hello" || workerContext[1] != "hi there" {
		t.Errorf("worker context = %v", workerContext)
	}

	events, err := db.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 3 { // program start + 2 messages
		t.Errorf("expected 3 events, got %d", len(events))
	}
}

func TestPrepareHistoryCompactsStaleHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenConversationDB(path)
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	defer db.Close()
	eventDB = db
	t.Cleanup(func() { eventDB = nil })

	old := time.Now().Add(-5 * time.Hour)
	must(t, db.InsertProgramStart(old))
	must(t, db.InsertMessage(old.Add(time.Minute), "user", "old question"))
	must(t, db.InsertMessage(old.Add(2*time.Minute), "assistant", "old answer"))

	// Stub the LLM compaction.
	priorCompaction := ""
	var gotTail []Event
	originalCompactFn := compactFn
	compactFn = func(prior string, tail []Event) (string, error) {
		priorCompaction = prior
		gotTail = make([]Event, len(tail))
		copy(gotTail, tail)
		return "new summary", nil
	}
	t.Cleanup(func() { compactFn = originalCompactFn })

	assistantContext, workerContext, err := prepareHistory(time.Now(), 3)
	if err != nil {
		t.Fatalf("prepareHistory: %v", err)
	}

	if priorCompaction != "" {
		t.Errorf("prior compaction = %q, want empty", priorCompaction)
	}
	if len(gotTail) != 2 {
		t.Errorf("expected 2 tail messages, got %d", len(gotTail))
	}

	// The assistant context is the new compaction only.
	if len(assistantContext) != 1 {
		t.Fatalf("expected 1 context message, got %d: %+v", len(assistantContext), assistantContext)
	}
	c := assistantContext[0]
	if c.Role != "system" || !strings.Contains(c.Content, "new summary") {
		t.Errorf("context[0] = %+v", c)
	}
	if !strings.Contains(c.Content, "[Compaction |") {
		t.Errorf("missing compaction header: %q", c.Content)
	}
	if len(workerContext) != 1 || workerContext[0] != c.Content {
		t.Errorf("worker context = %v", workerContext)
	}

	// A compaction event was appended.
	events, err := db.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 { // program start + 2 messages + compaction
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}
	last := events[3]
	if last.Type != EventCompaction || last.Content != "new summary" {
		t.Errorf("last event = %+v", last)
	}
}

func TestPrepareHistoryCompactsWithExistingCompaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenConversationDB(path)
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	defer db.Close()
	eventDB = db
	t.Cleanup(func() { eventDB = nil })

	old := time.Now().Add(-2 * time.Hour)
	must(t, db.InsertProgramStart(old))
	must(t, db.InsertMessage(old.Add(time.Minute), "user", "old question"))
	must(t, db.InsertCompaction(old.Add(2*time.Minute), "prior summary"))
	must(t, db.InsertMessage(old.Add(3*time.Minute), "user", "new question"))
	must(t, db.InsertMessage(old.Add(4*time.Minute), "assistant", "new answer"))

	originalCompactFn := compactFn
	var gotPrior string
	var gotTail []Event
	compactFn = func(prior string, tail []Event) (string, error) {
		gotPrior = prior
		gotTail = make([]Event, len(tail))
		copy(gotTail, tail)
		return "merged summary", nil
	}
	t.Cleanup(func() { compactFn = originalCompactFn })

	now := time.Now().Add(3*time.Hour + time.Minute) // more than X hours after the last message
	assistantContext, workerContext, err := prepareHistory(now, 3)
	if err != nil {
		t.Fatalf("prepareHistory: %v", err)
	}

	// The prior compaction and the messages after it are folded together.
	if gotPrior != "prior summary" {
		t.Errorf("prior = %q, want %q", gotPrior, "prior summary")
	}
	if len(gotTail) != 2 {
		t.Errorf("tail = %+v, want 2 messages", gotTail)
	}

	if len(assistantContext) != 1 || !strings.Contains(assistantContext[0].Content, "merged summary") {
		t.Errorf("assistant context = %+v", assistantContext)
	}
	if len(workerContext) != 1 || workerContext[0] != assistantContext[0].Content {
		t.Errorf("worker context = %v", workerContext)
	}
}

func TestPrepareHistoryCompactionFailureFallsBackToUncompacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenConversationDB(path)
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	defer db.Close()
	eventDB = db
	t.Cleanup(func() { eventDB = nil })

	old := time.Now().Add(-5 * time.Hour)
	must(t, db.InsertMessage(old, "user", "old question"))

	originalCompactFn := compactFn
	compactFn = func(prior string, tail []Event) (string, error) {
		return "", errCompactionStub
	}
	t.Cleanup(func() { compactFn = originalCompactFn })

	assistantContext, workerContext, err := prepareHistory(time.Now(), 3)
	if err != nil {
		t.Fatalf("prepareHistory: %v", err)
	}

	// Fallback: the un-compacted tail is kept.
	if len(assistantContext) != 1 || assistantContext[0].Content != "old question" {
		t.Errorf("assistant context = %+v", assistantContext)
	}
	if len(workerContext) != 1 || workerContext[0] != "old question" {
		t.Errorf("worker context = %v", workerContext)
	}

	// No compaction event was written.
	events, _ := db.Events()
	for _, ev := range events {
		if ev.Type == EventCompaction {
			t.Errorf("unexpected compaction event: %+v", ev)
		}
	}
}

func TestPrepareHistorySkipsCompactionWhenFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenConversationDB(path)
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	defer db.Close()
	eventDB = db
	t.Cleanup(func() { eventDB = nil })

	at := time.Now()
	must(t, db.InsertMessage(at, "user", "hello"))

	calls := 0
	originalCompactFn := compactFn
	compactFn = func(prior string, tail []Event) (string, error) {
		calls++
		return "would not be used", nil
	}
	t.Cleanup(func() { compactFn = originalCompactFn })

	assistantContext, _, err := prepareHistory(at.Add(30*time.Minute), 3)
	if err != nil {
		t.Fatalf("prepareHistory: %v", err)
	}
	if calls != 0 {
		t.Errorf("compaction should not run; got %d calls", calls)
	}
	if len(assistantContext) != 1 || assistantContext[0].Content != "hello" {
		t.Errorf("assistant context = %+v", assistantContext)
	}
}

func TestWorkerContextMessagesMatchAssistant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenConversationDB(path)
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	defer db.Close()
	eventDB = db
	t.Cleanup(func() { eventDB = nil })

	at := time.Now()
	must(t, db.InsertMessage(at, "user", "user1"))
	must(t, db.InsertMessage(at.Add(time.Minute), "assistant", "ai1"))

	assistantContext, workerContext, err := prepareHistory(at.Add(2*time.Minute), 3)
	if err != nil {
		t.Fatalf("prepareHistory: %v", err)
	}
	if len(assistantContext) != len(workerContext) {
		t.Fatalf("context sizes differ: %d vs %d", len(assistantContext), len(workerContext))
	}
	for i := range assistantContext {
		want := openrouter.Message{Role: assistantContext[i].Role, Content: workerContext[i]}
		if assistantContext[i].Content != want.Content {
			t.Errorf("message %d: assistant %q vs worker %q", i, assistantContext[i].Content, workerContext[i])
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
