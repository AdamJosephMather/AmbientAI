package main

import (
	"AmbientAI/openrouter"
	"AmbientAI/ui"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseGateCancelsAndRejectsStaleReplies(t *testing.T) {
	var gate responseGate
	first := gate.begin()
	second := gate.begin()
	if first.Err() != context.Canceled {
		t.Fatal("superseded request was not cancelled")
	}
	if gate.finish(first) || !gate.active() {
		t.Fatal("stale reply consumed the active turn")
	}
	gate.cancel() // User starts speaking, before transcription is available.
	if second.Err() != context.Canceled || gate.finish(second) {
		t.Fatal("speech did not invalidate the request")
	}
	latest := gate.begin()
	if !gate.finish(latest) || gate.finish(latest) || gate.active() {
		t.Fatal("latest reply was not accepted exactly once")
	}
}

func TestCancelledFrontResponseCannotMutateConversation(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(3 * time.Second):
			t.Error("HTTP request was not cancelled")
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"speech\":\"stale speech\",\"tools\":[]}"}}]}`)
	}))
	defer server.Close()
	oldChat, oldConfig, oldGame, oldHistory, oldGate := chat, aiConfig, game, mainChat, frontTurns
	t.Cleanup(func() { chat, aiConfig, game, mainChat, frontTurns = oldChat, oldConfig, oldGame, oldHistory, oldGate })
	chat = &openrouter.Chat{Config: openrouter.ChatConfig{URL: server.URL}, Client: server.Client()}
	aiConfig = &AIConfig{ModelFast: "test"}
	game = &ui.Game{}
	mainChat = []openrouter.Message{{Role: "user", Content: "original speech"}}
	ctx := frontTurns.begin()
	done := make(chan struct{})
	go func() { completeFrontResponse(ctx, append([]openrouter.Message(nil), mainChat...)); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	conversationMu.Lock()
	frontTurns.cancel()
	next := frontTurns.begin()
	conversationMu.Unlock()
	defer frontTurns.cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled request did not exit")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach HTTP server")
	}
	if len(mainChat) != 1 || frontTurns.ctx != next {
		t.Fatal("cancelled response changed history or replaced the next turn")
	}
}
