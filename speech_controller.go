package main

import (
	"AmbientAI/openrouter"
	"context"
	"errors"
	"strings"
	"sync"
)

type speechSnapshot struct {
	Active              bool     `json:"active"`
	CurrentSentence     string   `json:"current_sentence,omitempty"`
	RemainingSentences  []string `json:"remaining_sentences,omitempty"`
	QueuedResponses     []string `json:"queued_responses,omitempty"`
	RecentlyCompleted   []string `json:"recently_completed_sentences,omitempty"`
	InterruptedSentence string   `json:"last_interrupted_sentence,omitempty"`
}

// One playback owner prevents overlapping audio. Model requests use a separate
// lock, so decisions can run while this owner synthesizes or plays speech.
type speechController struct {
	mu          sync.Mutex
	queue       []string
	running     bool
	cancel      context.CancelFunc
	current     string
	remaining   []string
	completed   []string
	interrupted string
	synthesize  func(context.Context, string) (*openrouter.Speech, error)
	play        func(context.Context, []byte) error
	talking     func(bool)
	onError     func(error)
}

func (s *speechController) snapshot() speechSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return speechSnapshot{
		Active: s.running, CurrentSentence: s.current,
		RemainingSentences:  append([]string(nil), s.remaining...),
		QueuedResponses:     append([]string(nil), s.queue...),
		RecentlyCompleted:   append([]string(nil), s.completed...),
		InterruptedSentence: s.interrupted,
	}
}

func (s *speechController) submit(action, text string) {
	// Silence never cancels audio, even if an invalid caller requests it.
	if action == "keep" || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if action == "interrupt" {
		if s.cancel != nil {
			s.cancel()
			s.interrupted = s.current
		}
		s.queue = nil
	}
	s.queue = append(s.queue, text)
	if !s.running {
		s.running = true
		s.talking(true)
		go s.run()
	}
}

func (s *speechController) run() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.running = false
			s.talking(false)
			s.mu.Unlock()
			return
		}
		text := s.queue[0]
		s.queue = s.queue[1:]
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.remaining = speechChunks(text)
		s.mu.Unlock()

		err := speakResponseWithProgress(ctx, text, s.synthesize, s.play, func(sentence string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if ctx.Err() == nil {
				s.current = sentence
			}
		}, func(sentence string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if ctx.Err() == nil {
				s.completed = append(s.completed, sentence)
				if len(s.completed) > 8 {
					s.completed = s.completed[len(s.completed)-8:]
				}
				s.current = ""
				s.remaining = s.remaining[1:]
			}
		})
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.current = ""
		s.remaining = nil
		s.mu.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) {
			s.onError(err)
		}
	}
}
