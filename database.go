package main

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Event types stored in the database. Every row in the events table is one
// of these; messages carry a role of "user", "assistant" or "system".
const (
	EventProgramStart = "program_start"
	EventMessage      = "message"
	EventCompaction   = "compaction"
)

// Event is a single append-only database row. Time is stored as unix time.
type Event struct {
	ID      int64
	Time    time.Time
	Type    string
	Role    string
	Content string
}

const eventSchema = `
CREATE TABLE IF NOT EXISTS events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ts INTEGER NOT NULL,
    type TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL
);`

// ConversationDB is an append-only event log. Rows are only ever inserted
// and read back; they are never updated or deleted.
type ConversationDB struct {
	mu sync.Mutex
	db *sql.DB
}

func OpenConversationDB(path string) (*ConversationDB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open conversation database: %w", err)
	}
	// Single writer keeps the append-only log self-consistent.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(eventSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create events table: %w", err)
	}
	return &ConversationDB{db: db}, nil
}

// Insert appends one event to the log.
func (d *ConversationDB) Insert(ts time.Time, typ, role, content string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`INSERT INTO events (ts, type, role, content) VALUES (?, ?, ?, ?)`,
		ts.Unix(), typ, role, content)
	if err != nil {
		return fmt.Errorf("insert %s event: %w", typ, err)
	}
	return nil
}

func (d *ConversationDB) InsertProgramStart(at time.Time) error {
	return d.Insert(at, EventProgramStart, "system", "")
}

func (d *ConversationDB) InsertMessage(at time.Time, role, content string) error {
	return d.Insert(at, EventMessage, role, content)
}

func (d *ConversationDB) InsertCompaction(at time.Time, content string) error {
	return d.Insert(at, EventCompaction, "system", content)
}

// Events returns every event in append order.
func (d *ConversationDB) Events() ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query(`SELECT id, ts, type, role, content FROM events ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var (
			id      int64
			ts      int64
			typ     string
			role    string
			content string
		)
		if err := rows.Scan(&id, &ts, &typ, &role, &content); err != nil {
			return nil, fmt.Errorf("read event: %w", err)
		}
		events = append(events, Event{
			ID:      id,
			Time:    time.Unix(ts, 0).UTC(),
			Type:    typ,
			Role:    role,
			Content: content,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return events, nil
}

func (d *ConversationDB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.db.Close()
}
