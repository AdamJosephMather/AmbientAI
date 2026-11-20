package ui

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWindowUpdatesAndDeletion(t *testing.T) {
	g := &Game{buttonToWindow: make(map[int]int)}
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 100; n++ {
				id := g.AddWindow("initial", "", Rect{W: 300, H: 300})
				if err := g.UpdateWindow(id, fmt.Sprint(id), "updated"); err != nil {
					t.Error(err)
				}
				g.PullItUp(id)
				g.MoveWindowTo(id, Rect{W: 400, H: 300})
				if !g.CloseWindow(id) {
					t.Errorf("window %d was lost", id)
				}
				if err := g.UpdateWindow(id, "late", "late"); err == nil {
					t.Errorf("accepted update for closed window %d", id)
				}
			}
		}()
	}
	workers.Wait()
	if len(g.windows) != 0 {
		t.Fatalf("%d windows remain", len(g.windows))
	}
}

func TestCloseWindowDoesNotWaitForCallback(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	defer func() {
		close(release)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("callback did not finish")
		}
	}()
	g := &Game{buttonToWindow: make(map[int]int)}
	id := g.AddWindow("test", "", Rect{})
	g.WindowClosed = func(closedID int) {
		defer close(finished)
		close(started)
		<-release
	}
	closed := make(chan bool, 1)
	go func() { closed <- g.CloseWindow(id) }()
	select {
	case ok := <-closed:
		if !ok {
			t.Fatal("window was not closed")
		}
	case <-time.After(time.Second):
		t.Fatal("close waited for the plugin callback")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("callback did not run")
	}
}
