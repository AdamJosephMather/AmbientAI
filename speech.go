package main

import (
	"AmbientAI/openrouter"
	"context"
	"fmt"
	"strings"
	"unicode"
)

// Synthesize the next sentence while the current one plays. The unbuffered
// handoff bounds prefetch to one sentence and preserves playback order.
func speakResponse(ctx context.Context, text string, synthesize func(context.Context, string) (*openrouter.Speech, error), play func(context.Context, []byte) error) error {
	return speakResponseWithProgress(ctx, text, synthesize, play, nil, nil)
}

func speakResponseWithProgress(ctx context.Context, text string, synthesize func(context.Context, string) (*openrouter.Speech, error), play func(context.Context, []byte) error, started, finished func(string)) error {
	ctx, cancel := context.WithCancel(ctx)
	type result struct {
		text   string
		speech *openrouter.Speech
		err    error
	}
	results := make(chan result)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(results)
		for _, chunk := range speechChunks(text) {
			if ctx.Err() != nil {
				return
			}
			audio, err := synthesize(ctx, chunk)
			select {
			case results <- result{chunk, audio, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-done
	}()
	for result := range results {
		if result.err != nil {
			return fmt.Errorf("synthesize speech: %w", result.err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if started != nil {
			started(result.text)
		}
		if err := play(ctx, result.speech.Audio); err != nil {
			return fmt.Errorf("play speech: %w", err)
		}
		if finished != nil {
			finished(result.text)
		}
	}
	return ctx.Err()
}

func speechChunks(text string) []string {
	runes := []rune(strings.TrimSpace(text))
	var chunks []string
	for len(runes) > 0 {
		end := len(runes)
		for i, r := range runes {
			if r != '.' && r != '!' && r != '?' {
				continue
			}
			j := i + 1
			for j < len(runes) && strings.ContainsRune(".!?\"'”’)]}", runes[j]) {
				j++
			}
			if j < len(runes) && !unicode.IsSpace(runes[j]) {
				continue
			}
			if r == '.' && speechAbbreviation(runes[:i]) {
				continue
			}
			end = j
			break
		}
		// Respect the provider's 5000-character limit even without punctuation.
		if end > 5000 {
			end = 5000
			for i := end; i > 0; i-- {
				if unicode.IsSpace(runes[i]) {
					end = i
					break
				}
			}
		}
		if chunk := strings.TrimSpace(string(runes[:end])); chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[end:]
		for len(runes) > 0 && unicode.IsSpace(runes[0]) {
			runes = runes[1:]
		}
	}
	return chunks
}

func speechAbbreviation(beforePeriod []rune) bool {
	start := len(beforePeriod)
	for start > 0 && (unicode.IsLetter(beforePeriod[start-1]) || beforePeriod[start-1] == '.') {
		start--
	}
	word := string(beforePeriod[start:])
	if len([]rune(word)) == 1 && unicode.IsLetter(beforePeriod[start]) {
		return true
	}
	switch strings.ToLower(word) {
	case "mr", "mrs", "ms", "dr", "prof", "sr", "jr", "e.g", "i.e", "a.m", "p.m", "u.s", "u.k":
		return true
	}
	return false
}
