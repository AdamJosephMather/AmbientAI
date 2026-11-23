package main

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *ConversationDB {
	t.Helper()
	db, err := OpenConversationDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenConversationDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInsertAndEvents(t *testing.T) {
	db := openTestDB(t)
	at := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

	if err := db.InsertProgramStart(at); err != nil {
		t.Fatalf("InsertProgramStart: %v", err)
	}
	if err := db.InsertMessage(at.Add(time.Minute), "user", "[From: STT | 10:01]\n\nhello"); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := db.InsertMessage(at.Add(2*time.Minute), "assistant", "hi"); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := db.InsertCompaction(at.Add(3*time.Minute), "summary text"); err != nil {
		t.Fatalf("InsertCompaction: %v", err)
	}

	events, err := db.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	if events[0].Type != EventProgramStart || events[0].Content != "" {
		t.Errorf("event 0 = %+v, want program_start", events[0])
	}
	if events[1].Type != EventMessage || events[1].Role != "user" || events[1].Content != "[From: STT | 10:01]\n\nhello" {
		t.Errorf("event 1 = %+v", events[1])
	}
	if events[2].Type != EventMessage || events[2].Role != "assistant" {
		t.Errorf("event 2 = %+v", events[2])
	}
	if events[3].Type != EventCompaction || events[3].Role != "user" || events[3].Content != "summary text" {
		t.Errorf("event 3 = %+v", events[3])
	}

	for i, ev := range events {
		if !ev.Time.Equal(at.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("event %d time = %v, want %v", i, ev.Time, at.Add(time.Duration(i)*time.Minute))
		}
		if ev.ID == 0 {
			t.Errorf("event %d missing id", i)
		}
	}
}

func TestEventsPreserveAppendOrder(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 10; i++ {
		if err := db.InsertMessage(time.Now(), "user", "msg"); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}
	events, err := db.Events()
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	for i := 1; i < len(events); i++ {
		if events[i].ID <= events[i-1].ID {
			t.Errorf("events not in append order: %d then %d", events[i-1].ID, events[i].ID)
		}
	}
}
