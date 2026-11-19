package ui

import (
	"testing"
	"time"
)

func TestSizeChangeNotifier(t *testing.T) {
	type step struct {
		at             time.Duration
		width, height  int
		resizing, want bool
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "initial size is sent once",
			steps: []step{
				{0, 0, 0, false, false},
				{time.Millisecond, 640, 480, false, true},
				{time.Second, 640, 480, false, false},
			},
		},
		{
			name: "drag waits through a pause until release",
			steps: []step{
				{0, 640, 480, false, true},
				{time.Millisecond, 650, 480, true, false},
				{time.Second, 650, 480, true, false},
				{2 * time.Second, 800, 600, true, false},
				{3 * time.Second, 800, 600, false, true},
				{4 * time.Second, 800, 600, false, false},
			},
		},
		{
			name: "fullscreen and system resize bursts settle at final size",
			steps: []step{
				{0, 640, 480, false, true},
				{10 * time.Millisecond, 1000, 700, false, false},
				{100 * time.Millisecond, 1920, 1080, false, false},
				{249 * time.Millisecond, 1920, 1080, false, false},
				{250 * time.Millisecond, 1920, 1080, false, true},
				{time.Second, 1920, 1080, false, false},
				{2 * time.Second, 640, 480, false, false},
				{3 * time.Second, 640, 480, false, true},
			},
		},
		{
			name: "release with a final size waits for that size to settle",
			steps: []step{
				{0, 640, 480, true, false},
				{time.Second, 800, 600, false, false},
				{time.Second + sizeChangeSettleDelay, 800, 600, false, true},
				{2 * time.Second, 800, 600, false, false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var notifier sizeChangeNotifier
			start := time.Unix(0, 0)
			for i, step := range tt.steps {
				if got := notifier.update(step.width, step.height, start.Add(step.at), step.resizing); got != step.want {
					t.Fatalf("step %d: update() = %v, want %v", i, got, step.want)
				}
			}
		})
	}
}
