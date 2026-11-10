package ui

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hajimehoshi/ebiten/v2/text/v2"
	extast "github.com/yuin/goldmark/extension/ast"
	gmtext "github.com/yuin/goldmark/text"
)

func markdownTestGame(t *testing.T) *Game {
	t.Helper()
	g := &Game{}
	g.SetupTheme()
	if err := g.SetupText(17); err != nil {
		t.Fatal(err)
	}
	return g
}

func markdownText(elements []Element) string {
	var b strings.Builder
	for _, el := range elements {
		if el.kind == elementText {
			b.WriteString(el.text)
		}
	}
	return b.String()
}

func TestMarkdownInlineSemantics(t *testing.T) {
	cases := []struct{ source, want string }{
		{`**bold and *italic*** plus ~~gone~~`, "bold and italic plus gone"},
		{`\*literal\* &amp; &#65; &copy; \&amp; &amp;copy;`, "*literal* & A © &amp; &copy;"},
		{"` a  &amp; *b* `", "a  &amp; *b*"},
		{"first\nsecond  \nthird\\\nfourth", "first second\nthird\nfourth"},
		{"[label][ref] and <https://example.com>\n\n[ref]: https://example.com", "label and https://example.com"},
		{"![an *image*](https://example.com/pic.png)", "[Image: an image]"},
		{"unclosed **bold and `code", "unclosed **bold and `code"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			source := []byte(tc.source)
			doc := markdownParser.Parser().Parse(gmtext.NewReader(source))
			var result strings.Builder
			for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
				for _, r := range markdownInlines(n, source, markdownStyle{}) {
					result.WriteString(r.text)
				}
			}
			if got := result.String(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	source := []byte("***both*** ~~gone~~ [link](https://example.com) `code`")
	doc := markdownParser.Parser().Parse(gmtext.NewReader(source))
	runs := markdownInlines(doc.FirstChild(), source, markdownStyle{})
	styles := map[string]markdownStyle{}
	for _, r := range runs {
		styles[r.text] = r.style
	}
	if !styles["both"].bold || !styles["both"].italic || !styles["gone"].strike || !styles["link"].link || !styles["code"].code {
		t.Fatalf("missing inline styles: %+v", styles)
	}
}

func TestMarkdownLayoutBlocks(t *testing.T) {
	g := markdownTestGame(t)
	body := "# Heading\n\nPlain **bold** *italic* ***both*** ~~gone~~ [link](https://example.com).\n\n---\n\n> A quote\n>\n> > Nested quote\n\n3. First\n4. Second\n   - Nested\n\n- [x] Done\n- [ ] Pending\n\n```go\n  x := \"**literal**\"\n\n  return x\n```\n\n    indented\n\n| Left | Center | Right |\n| :--- | :---: | ---: |\n| wrapped **content** here | middle | end |\n\nSetext\n======\n\n### Three\n#### Four\n##### Five\n###### Six\n"
	w := &Window{body: body, rect: Rect{w: 330, h: 300}}
	g.layoutWindowMarkdown(w)
	var headings [7]bool
	rects := 0
	for _, el := range w.elements {
		if el.kind == elementRect {
			rects++
			continue
		}
		headings[el.style.heading] = true
		if !utf8.ValidString(el.text) {
			t.Fatalf("invalid UTF-8: %q", el.text)
		}
		if math.IsNaN(float64(el.xoffset+el.yoffset+el.w+el.h)) || el.w < 0 || el.h <= 0 {
			t.Fatalf("invalid element: %+v", el)
		}
	}
	for _, i := range []int{1, 3, 4, 5, 6} {
		if !headings[i] {
			t.Errorf("missing heading %d", i)
		}
	}
	if rects < 15 {
		t.Errorf("missing block decorations: %d rectangles", rects)
	}
	for _, want := range []string{"3.", "4.", "•", "[x]", "[ ]", "**literal**", "indented", "content"} {
		if !strings.Contains(markdownText(w.elements), want) {
			t.Errorf("missing %q", want)
		}
	}
	if w.bodyheight < 300 {
		t.Fatalf("content height too short: %f", w.bodyheight)
	}
}

func TestMarkdownWrappingAndBaselines(t *testing.T) {
	g := markdownTestGame(t)
	l := markdownLayout{game: g, faces: make(map[markdownStyle]*text.GoTextFace), lineHeight: 20}
	normal := markdownStyle{}
	bold := markdownStyle{bold: true}
	width := l.measure("prefix ", normal) + l.measure("format", normal) + l.measure("ted", bold) - 1
	l.flow([]markdownRun{{"prefix format", normal}, {"ted", bold}}, 0, 0, width, false, extast.AlignLeft)
	var prefix, format, ted Element
	for _, el := range l.elements {
		switch el.text {
		case "prefix":
			prefix = el
		case "format":
			format = el
		case "ted":
			ted = el
		}
	}
	if format.yoffset <= prefix.yoffset || format.yoffset != ted.yoffset || format.xoffset != 0 {
		t.Fatalf("word split across styles: %+v", l.elements)
	}
	l.elements = nil
	big := markdownStyle{heading: 1, bold: true}
	l.flow([]markdownRun{{"big ", big}, {"small", normal}}, 0, 0, 300, false, extast.AlignLeft)
	var baseline float32 = -1
	for _, el := range l.elements {
		current := el.yoffset + float32(l.face(el.style).Metrics().HAscent)
		if baseline >= 0 && math.Abs(float64(current-baseline)) > 0.01 {
			t.Fatalf("misaligned baselines: %+v", l.elements)
		}
		baseline = current
	}
}

func TestMarkdownCodeWhitespace(t *testing.T) {
	g := markdownTestGame(t)
	cases := []struct{ body, want string }{
		{"` a  b `", "a  b"},
		{"```\n  a  b\n\n    c\n```", "  a  b    c"},
		{"```\n\tfoo\n```", "    foo"},
		{"```\nab\tc\n```", "ab  c"},
	}
	for _, tc := range cases {
		w := &Window{body: tc.body, rect: Rect{w: 400}}
		g.layoutWindowMarkdown(w)
		if got := markdownText(w.elements); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.body, got, tc.want)
		}
	}
	w := &Window{body: "```\n" + strings.Repeat(" ", 200) + "x\n```", rect: Rect{w: 100}}
	g.layoutWindowMarkdown(w)
	if got := markdownText(w.elements); got != strings.Repeat(" ", 200)+"x" {
		t.Fatalf("lost wrapped code whitespace: %q", got)
	}
}

func TestMarkdownTableAlignmentAndRowHeight(t *testing.T) {
	g := markdownTestGame(t)
	w := &Window{body: "| L | C | R |\n| :--- | :---: | ---: |\n| a long cell that wraps across several lines | c | r |\n| next | next | next |", rect: Rect{w: 300}}
	g.layoutWindowMarkdown(w)
	var c, r, next Element
	bottom := float32(0)
	for _, el := range w.elements {
		if el.kind != elementText {
			continue
		}
		switch el.text {
		case "c":
			c = el
		case "r":
			r = el
		case "next":
			next = el
		default:
			if !el.style.bold {
				bottom = max(bottom, el.yoffset+el.h)
			}
		}
	}
	col := (w.rect.w - 4*g.stripSize) / 3
	if c.xoffset <= g.stripSize+col+col/3 {
		t.Fatalf("center cell not centered: %+v", c)
	}
	if r.xoffset+r.w < g.stripSize+3*col-2*g.stripSize {
		t.Fatalf("right cell not right aligned: %+v", r)
	}
	if next.yoffset < bottom {
		t.Fatalf("next row overlaps wrapped cell: next=%+v bottom=%f", next, bottom)
	}
}

func TestMarkdownResizeAndContentInvalidation(t *testing.T) {
	g := markdownTestGame(t)
	w := &Window{body: strings.Repeat("word **bold** ", 30), rect: Rect{w: 400}}
	g.layoutWindowMarkdown(w)
	wideHeight := w.bodyheight
	first := &w.elements[0]
	g.layoutWindowMarkdown(w)
	if &w.elements[0] != first {
		t.Fatal("unchanged content was laid out again")
	}
	w.rect.w = 140
	g.layoutWindowMarkdown(w)
	if w.bodyheight <= wideHeight {
		t.Fatal("narrower width did not reflow")
	}
	w.body = "# replacement"
	g.layoutWindowMarkdown(w)
	if got := markdownText(w.elements); got != "replacement" {
		t.Fatalf("stale content: %q", got)
	}
	g.windows = []Window{*w}
	if err := g.SetupText(24); err != nil {
		t.Fatal(err)
	}
	w = &g.windows[0]
	if w.markdownFaces != nil {
		t.Fatal("font change did not invalidate cached layout")
	}
	g.layoutWindowMarkdown(w)
	if face := w.markdownFaces[markdownStyle{heading: 1, bold: true}]; face == nil || face.Size != 24*1.8 {
		t.Fatalf("stale heading face: %+v", face)
	}
}

func TestMarkdownNarrowAndEmptyWindows(t *testing.T) {
	g := markdownTestGame(t)
	for _, body := range []string{"", "\n\n", "中文🙂é **hello**", strings.Repeat("x", 3000), "| one | two | three |\n|---|---|---|\n| a | b | c |", "> > > - nested"} {
		for _, width := range []float32{0, 1, 30, 100} {
			w := &Window{body: body, rect: Rect{w: width}}
			g.layoutWindowMarkdown(w)
			if w.bodyheight <= 0 || math.IsInf(float64(w.bodyheight), 0) {
				t.Fatalf("invalid height for %q at %f", body, width)
			}
			for _, el := range w.elements {
				if !utf8.ValidString(el.text) {
					t.Fatal("wrapping split UTF-8")
				}
			}
		}
	}
}

func TestMarkdownScrollOverflow(t *testing.T) {
	w := Window{rect: Rect{y: 50, h: 300}, titleBarStrip: 90, bodyheight: 600}
	if got := w.scrollOverflow(3); got != 346 {
		t.Fatalf("overflow=%f", got)
	}
	w.bodyheight = 50
	if got := w.scrollOverflow(3); got != 0 {
		t.Fatalf("short content overflow=%f", got)
	}
}

// Ensure parser-only constructs are consumed as blocks, not leaked as text.
func TestMarkdownReferenceDefinitions(t *testing.T) {
	g := markdownTestGame(t)
	w := &Window{body: "[ref]: https://example.com\n\n## Heading\n\n[Text][ref]", rect: Rect{w: 300}}
	g.layoutWindowMarkdown(w)
	if got := markdownText(w.elements); got != "HeadingText" {
		t.Fatalf("reference definition leaked: %q", got)
	}
	if len(w.elements) == 0 || w.elements[0].yoffset != g.stripSize {
		t.Fatalf("reference definition left blank space: %+v", w.elements)
	}
}
