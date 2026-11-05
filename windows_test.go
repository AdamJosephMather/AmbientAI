package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestWindowInteractions(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	desktop := container.NewMultipleWindows()
	host := test.NewWindow(desktop)
	defer host.Close()
	host.SetPadded(false)
	host.Resize(fyne.NewSize(1000, 650))

	first := createWindow("Research", "## Findings\n\n**Important** details\n\n- First item\n- Second item\n\n[Source](https://example.com)")
	second := createWindow("Notes", "A separate window.")
	placeWindow(desktop, first)
	placeWindow(desktop, second)
	if first.Position() == second.Position() || desktop.Top() != second {
		t.Fatal("new windows should cascade and appear on top")
	}

	// Tap the exposed title bar, then drag it without moving the other window.
	test.TapCanvas(host.Canvas(), first.Position().AddXY(80, 13))
	if desktop.Top() != first {
		t.Fatal("tapping the title bar should raise the window")
	}
	before, other := first.Position(), second.Position()
	test.Drag(host.Canvas(), first.Position().AddXY(80, 13), 100, 60)
	if first.Position() != before.AddXY(100, 60) || second.Position() != other {
		t.Fatal("title bar drag should move only the selected window")
	}

	beforeSize := first.Size()
	corner := first.Position().AddXY(beforeSize.Width-5, beforeSize.Height-5)
	test.Drag(host.Canvas(), corner, 80, 40)
	if first.Size() != beforeSize.Add(fyne.Delta{DX: 80, DY: 40}) {
		t.Fatal("dragging the bottom-right corner should resize the window")
	}
	first.OnResized(&fyne.DragEvent{Dragged: fyne.Delta{DX: -2000, DY: -2000}})
	if first.Size().Width < first.MinSize().Width || first.Size().Height < first.MinSize().Height {
		t.Fatal("resizing should respect the minimum window size")
	}

	first.CloseIntercept()
	if len(desktop.Windows) != 1 || desktop.Top() != second {
		t.Fatal("closing a window should remove it from the workspace")
	}
}

func TestWindowMarkdown(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := createWindow("Compact title", "## Heading\n\n**Bold** and *italic*\n\n- Item\n\n[Link](https://example.com)\n\n```go\nfmt.Println(42)\n```")
	body := w.Content.Objects[0].(*container.Scroll).Content.(*widget.RichText)
	if body.Wrapping != fyne.TextWrapWord {
		t.Fatal("Markdown should wrap as the window is resized")
	}
	var heading, bold, italic, list, link, code bool
	for _, segment := range body.Segments {
		switch s := segment.(type) {
		case *widget.TextSegment:
			heading = heading || s.Text == "Heading"
			bold = bold || (s.Text == "Bold" && s.Style.TextStyle.Bold)
			italic = italic || (s.Text == "italic" && s.Style.TextStyle.Italic)
			code = code || s.Style.TextStyle.Monospace
		case *widget.ListSegment:
			list = true
		case *widget.HyperlinkSegment:
			link = s.URL.String() == "https://example.com"
		case *widget.CodeBlockSegment:
			code = strings.TrimSpace(s.Text) == "fmt.Println(42)"
		}
	}
	if !heading || !bold || !italic || !list || !link || !code {
		t.Fatalf("missing Markdown formatting: heading=%v bold=%v italic=%v list=%v link=%v code=%v", heading, bold, italic, list, link, code)
	}
}
