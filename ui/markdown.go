package ui

// Markdown is parsed into an AST, then laid out into window-relative drawing
// primitives. DrawElements never parses Markdown or measures text.
import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"image"
	"image/color"
	"strings"
	"unicode"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	gmtext "github.com/yuin/goldmark/text"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/gofont/goregular"
)

type elementKind uint8

const (
	elementText elementKind = iota
	elementRect
)

type elementInk uint8

const (
	inkText elementInk = iota
	inkAccent
	inkBackground
)

type markdownStyle struct {
	bold, italic, code, strike, link bool
	heading                          int
}

type markdownRun struct {
	text  string
	style markdownStyle
}

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

func (g *Game) setupMarkdownFonts(size int) error {
	fonts := [][]byte{goregular.TTF, gobold.TTF, goitalic.TTF, gobolditalic.TTF, gomono.TTF, gomonobold.TTF, gomonoitalic.TTF, gomonobolditalic.TTF}
	for i, data := range fonts {
		source, err := text.NewGoTextFaceSource(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("load Markdown font: %w", err)
		}
		g.markdownSources[i] = source
	}
	g.faceSource = g.markdownSources[0]
	g.face = &text.GoTextFace{Source: g.faceSource, Size: float64(size)}
	for i := range g.windows {
		g.windows[i].markdownFaces = nil
	}
	return nil
}

func (g *Game) markdownFace(style markdownStyle, cache map[markdownStyle]*text.GoTextFace) *text.GoTextFace {
	if face := cache[style]; face != nil {
		return face
	}
	index := 0
	if style.bold {
		index = 1
	}
	if style.italic {
		index += 2
	}
	if style.code {
		index += 4
	}
	size := g.face.Size
	if style.heading > 0 {
		size *= []float64{1, 1.8, 1.55, 1.35, 1.2, 1.1, 1}[style.heading]
	}
	face := &text.GoTextFace{Source: g.markdownSources[index], Size: size}
	cache[style] = face
	return face
}

func markdownUnescape(value []byte) string {
	// Reuse the parser's text writer to preserve escape/entity ordering, then
	// decode its HTML output once to obtain the actual display text.
	var b bytes.Buffer
	writer := bufio.NewWriter(&b)
	gmhtml.DefaultWriter.Write(writer, value)
	_ = writer.Flush()
	return html.UnescapeString(b.String())
}

func markdownInlines(parent ast.Node, source []byte, style markdownStyle) []markdownRun {
	var runs []markdownRun
	var walk func(ast.Node, markdownStyle)
	add := func(value string, style markdownStyle) {
		if value != "" {
			runs = append(runs, markdownRun{value, style})
		}
	}
	walk = func(node ast.Node, style markdownStyle) {
		switch n := node.(type) {
		case *ast.Text:
			value := string(n.Segment.Value(source))
			if !n.IsRaw() {
				value = markdownUnescape(n.Segment.Value(source))
			}
			add(value, style)
			if n.HardLineBreak() {
				add("\n", style)
			} else if n.SoftLineBreak() {
				add(" ", style)
			}
			return
		case *ast.String:
			value := string(n.Value)
			if !n.IsRaw() && !n.IsCode() {
				value = markdownUnescape(n.Value)
			}
			add(value, style)
			return
		case *ast.Emphasis:
			if n.Level == 2 {
				style.bold = true
			} else {
				style.italic = true
			}
		case *extast.Strikethrough:
			style.strike = true
		case *ast.CodeSpan:
			style.code = true
			var b strings.Builder
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				t := c.(*ast.Text)
				b.WriteString(strings.ReplaceAll(string(t.Segment.Value(source)), "\n", " "))
			}
			add(b.String(), style)
			return
		case *ast.Link:
			style.link = true
		case *ast.AutoLink:
			style.link = true
			add(string(n.Label(source)), style)
			return
		case *ast.Image:
			add("[Image: ", style)
			for c := n.FirstChild(); c != nil; c = c.NextSibling() {
				walk(c, style)
			}
			add("]", style)
			return
		case *extast.TaskCheckBox:
			style.code = true
			if n.IsChecked {
				add("[x] ", style)
			} else {
				add("[ ] ", style)
			}
			return
		case *ast.RawHTML:
			// HTML is displayed literally; this renderer does not execute or fetch it.
			add(string(n.Segments.Value(source)), style)
			return
		}
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			walk(c, style)
		}
	}
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		walk(c, style)
	}
	return runs
}

type markdownLayout struct {
	game                     *Game
	source                   []byte
	faces                    map[markdownStyle]*text.GoTextFace
	elements                 []Element
	lineHeight, gap, padding float32
}

func (l *markdownLayout) face(s markdownStyle) *text.GoTextFace {
	return l.game.markdownFace(s, l.faces)
}
func (l *markdownLayout) measure(s string, style markdownStyle) float32 {
	w, _ := text.Measure(s, l.face(style), 0)
	return float32(w)
}
func (l *markdownLayout) rect(x, y, w, h float32, ink elementInk) {
	if w > 0 && h > 0 {
		l.elements = append(l.elements, Element{xoffset: x, yoffset: y, w: w, h: h, kind: elementRect, ink: ink})
	}
}

// flow wraps at whitespace across style boundaries. Only words wider than the
// entire line fall back to rune wrapping, so long URLs and CJK text still fit.
// Every run on a line shares a baseline, even when font sizes differ.
func (l *markdownLayout) flow(runs []markdownRun, x, y, width float32, preserve bool, align extast.Alignment) float32 {
	width = max(float32(1), width)
	startY, cursor := y, float32(0)
	lineStart := len(l.elements)
	ascent, height := float32(0), l.lineHeight
	var word, spaces []markdownRun
	lineOpen := false
	emit := func(run markdownRun) {
		if run.text == "" {
			return
		}
		face := l.face(run.style)
		metrics := face.Metrics()
		w := l.measure(run.text, run.style)
		l.elements = append(l.elements, Element{xoffset: x + cursor, yoffset: y, text: run.text, style: run.style, w: w, h: float32(metrics.HAscent + metrics.HDescent + metrics.HLineGap)})
		cursor += w
		ascent = max(ascent, float32(metrics.HAscent))
		height = max(height, float32(metrics.HAscent+metrics.HDescent+metrics.HLineGap))
		lineOpen = true
	}
	flush := func() {
		shift := float32(0)
		if align == extast.AlignRight {
			shift = max(float32(0), width-cursor)
		}
		if align == extast.AlignCenter {
			shift = max(float32(0), (width-cursor)/2)
		}
		for i := lineStart; i < len(l.elements); i++ {
			el := &l.elements[i]
			el.xoffset += shift
			el.yoffset += ascent - float32(l.face(el.style).Metrics().HAscent)
		}
		y += height
		cursor, ascent, height = 0, 0, l.lineHeight
		lineStart = len(l.elements)
		lineOpen = false
	}
	runWidth := func(runs []markdownRun) float32 {
		var w float32
		for _, r := range runs {
			w += l.measure(r.text, r.style)
		}
		return w
	}
	emitSpaces := func() {
		for _, s := range spaces {
			if cursor == 0 && !preserve && !s.style.code {
				continue
			}
			for _, r := range s.text {
				part := markdownRun{string(r), s.style}
				if cursor > 0 && cursor+l.measure(part.text, part.style) > width {
					flush()
				}
				emit(part)
			}
		}
		spaces = nil
	}
	putWord := func() {
		if len(word) == 0 {
			return
		}
		wordWidth, spaceWidth := runWidth(word), runWidth(spaces)
		if !preserve && cursor > 0 && cursor+spaceWidth+wordWidth > width {
			flush()
			spaces = nil
		}
		emitSpaces()
		if wordWidth <= width {
			if cursor > 0 && cursor+wordWidth > width {
				flush()
			}
			for _, r := range word {
				emit(r)
			}
		} else {
			for _, run := range word {
				for _, r := range run.text {
					part := markdownRun{string(r), run.style}
					if cursor > 0 && cursor+l.measure(part.text, part.style) > width {
						flush()
					}
					emit(part)
				}
			}
		}
		word = nil
	}
	for _, run := range runs {
		start := 0
		for i, r := range run.text {
			if !unicode.IsSpace(r) || r == '\u00a0' {
				continue
			}
			if start < i {
				word = append(word, markdownRun{run.text[start:i], run.style})
			}
			start = i + len(string(r))
			putWord()
			if r == '\n' {
				if preserve {
					emitSpaces()
				}
				spaces = nil
				flush()
			} else if preserve || run.style.code {
				spaces = append(spaces, markdownRun{string(r), run.style})
			} else if len(spaces) == 0 {
				spaces = append(spaces, markdownRun{" ", run.style})
			}
		}
		if start < len(run.text) {
			word = append(word, markdownRun{run.text[start:], run.style})
		}
	}
	putWord()
	if preserve || (len(spaces) > 0 && spaces[0].style.code) {
		emitSpaces()
	}
	if lineOpen {
		flush()
	}
	if y == startY {
		y += l.lineHeight
	}
	return y
}

func (l *markdownLayout) blocks(parent ast.Node, x, y, width float32) float32 {
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		y = l.block(node, x, y, width)
	}
	return y
}

func (l *markdownLayout) block(node ast.Node, x, y, width float32) float32 {
	width = max(float32(1), width)
	switch n := node.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		if node.FirstChild() == nil {
			return y
		}
		y = l.flow(markdownInlines(node, l.source, markdownStyle{}), x, y, width, false, extast.AlignLeft)
		if _, ok := node.(*ast.Paragraph); ok {
			y += l.gap
		}
	case *ast.Heading:
		y = l.flow(markdownInlines(node, l.source, markdownStyle{bold: true, heading: n.Level}), x, y, width, false, extast.AlignLeft) + l.gap
	case *ast.ThematicBreak:
		l.rect(x, y+l.gap, width, 1, inkAccent)
		y += 2*l.gap + 1
	case *ast.Blockquote:
		top := y
		indent := min(l.padding*3, width/3)
		y = l.blocks(node, x+indent, y, width-indent)
		l.rect(x, top, 2, max(l.lineHeight, y-top-l.gap), inkAccent)
		y += l.gap / 2
	case *ast.List:
		number := n.Start
		indent := l.lineHeight * 1.3
		if n.IsOrdered() {
			indent = max(indent, l.measure(fmt.Sprintf("%d.", number+n.ChildCount()-1), markdownStyle{})+l.padding)
		}
		indent = min(indent, width/2)
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			marker := "•"
			if n.IsOrdered() {
				marker = fmt.Sprintf("%d%c", number, n.Marker)
				number++
			}
			top := y
			markerY := l.flow([]markdownRun{{marker, markdownStyle{}}}, x, y, indent, false, extast.AlignLeft)
			y = l.blocks(item, x+indent, y, width-indent)
			y = max(y, markerY)
			if y == top {
				y += l.lineHeight
			}
			if !n.IsTight {
				y += l.gap / 2
			}
		}
		y += l.gap / 2
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		// Reserve the background before text so it draws behind every wrapped line.
		background := len(l.elements)
		l.elements = append(l.elements, Element{})
		top := y
		pad := min(l.padding, width/4)
		y += pad
		if fence, ok := node.(*ast.FencedCodeBlock); ok {
			if language := string(fence.Language(l.source)); language != "" {
				y = l.flow([]markdownRun{{language, markdownStyle{code: true, bold: true}}}, x+pad, y, width-2*pad, true, extast.AlignLeft) + l.gap/2
			}
		}
		value := expandMarkdownTabs(string(node.Lines().Value(l.source)))
		value = strings.TrimSuffix(value, "\n")
		y = l.flow([]markdownRun{{value, markdownStyle{code: true}}}, x+pad, y, width-2*pad, true, extast.AlignLeft) + pad
		l.elements[background] = Element{xoffset: x, yoffset: top, w: width, h: y - top, kind: elementRect, ink: inkBackground}
		y += l.gap
	case *extast.Table:
		y = l.table(n, x, y, width) + l.gap
	case *ast.HTMLBlock:
		value := string(n.Lines().Value(l.source))
		if n.HasClosure() {
			value += string(n.ClosureLine.Value(l.source))
		}
		y = l.flow([]markdownRun{{strings.TrimSuffix(value, "\n"), markdownStyle{code: true}}}, x, y, width, true, extast.AlignLeft) + l.gap
	default:
		y = l.blocks(node, x, y, width)
	}
	return y
}

// Code uses four-column tab stops, independent of where soft wrapping occurs.
func expandMarkdownTabs(value string) string {
	var b strings.Builder
	column := 0
	for _, r := range value {
		switch r {
		case '\t':
			count := 4 - column%4
			b.WriteString(strings.Repeat(" ", count))
			column += count
		case '\n':
			b.WriteRune(r)
			column = 0
		default:
			b.WriteRune(r)
			column++
		}
	}
	return b.String()
}

func (l *markdownLayout) table(table *extast.Table, x, y, width float32) float32 {
	columns := len(table.Alignments)
	if columns == 0 {
		return y
	}
	// Equal-width columns stay readable as windows resize. Cells independently
	// wrap; the tallest cell determines the row height. Alignment is per line.
	cellWidth := width / float32(columns)
	pad := min(l.padding, cellWidth/4)
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		_, header := row.(*extast.TableHeader)
		background := len(l.elements)
		l.elements = append(l.elements, Element{})
		bottom := y + l.lineHeight + 2*pad
		column := 0
		for cell := row.FirstChild(); cell != nil && column < columns; cell = cell.NextSibling() {
			end := l.flow(markdownInlines(cell, l.source, markdownStyle{bold: header}), x+float32(column)*cellWidth+pad, y+pad, cellWidth-2*pad, false, table.Alignments[column])
			bottom = max(bottom, end+pad)
			column++
		}
		// Body rows use no fill; a zero-size rectangle is a harmless placeholder.
		l.elements[background] = Element{kind: elementRect}
		if header {
			l.elements[background] = Element{xoffset: x, yoffset: y, w: width, h: bottom - y, kind: elementRect, ink: inkBackground}
		}
		l.rect(x, y, width, 1, inkAccent)
		l.rect(x, bottom-1, width, 1, inkAccent)
		for c := 0; c <= columns; c++ {
			l.rect(min(x+float32(c)*cellWidth, x+width-1), y, 1, bottom-y, inkAccent)
		}
		y = bottom
	}
	return y
}

func (g *Game) layoutWindowMarkdown(w *Window) {
	if w.oldwidth == w.rect.w && w.laidOutBody == w.body && w.markdownFaces != nil {
		return
	}
	source := []byte(strings.ReplaceAll(strings.ReplaceAll(w.body, "\r\n", "\n"), "\r", "\n"))
	doc := markdownParser.Parser().Parse(gmtext.NewReader(source))
	m := g.face.Metrics()
	l := markdownLayout{game: g, source: source, faces: make(map[markdownStyle]*text.GoTextFace), lineHeight: float32(m.HAscent + m.HDescent + m.HLineGap), gap: g.textHeight * 0.45, padding: max(float32(2), g.stripSize)}
	y := l.blocks(doc, g.stripSize, g.stripSize, max(float32(1), w.rect.w-4*g.stripSize))
	w.elements = l.elements
	w.markdownFaces = l.faces
	w.bodyheight = y + g.stripSize
	w.oldwidth = w.rect.w
	w.laidOutBody = w.body
}

func (w *Window) scrollOverflow(padding float32) float32 {
	visible := max(float32(0), w.rect.y+w.rect.h-w.titleBarStrip-2*padding)
	return max(float32(0), w.bodyheight-visible)
}

func (g *Game) DrawElements(screen *ebiten.Image, w *Window) {
	offsetX := w.rect.x + g.stripSize
	offsetY := w.titleBarStrip + g.stripSize - w.scroll*w.scrollOverflow(g.stripSize)
	clip := image.Rect(int(w.rect.x+g.stripSize), int(w.titleBarStrip+g.stripSize), int(w.rect.x+w.rect.w-g.stripSize), int(w.rect.y+w.rect.h-g.stripSize)).Intersect(screen.Bounds())
	if clip.Empty() {
		return
	}
	clipped := screen.SubImage(clip).(*ebiten.Image)
	background := blendMarkdownColor(g.theme.windowBackground, g.theme.borderColor, 0.13)
	for _, el := range w.elements {
		x, y := offsetX+el.xoffset, offsetY+el.yoffset
		if y+el.h < float32(clip.Min.Y) || y > float32(clip.Max.Y) {
			continue
		}
		if el.kind == elementRect {
			c := g.theme.whiteColor
			if el.ink == inkAccent {
				c = g.theme.borderColor
			}
			if el.ink == inkBackground {
				c = background
			}
			if el.w > 0 && el.h > 0 {
				g.DrawRect(clipped, Rect{x, y, el.w, el.h}, c)
			}
			continue
		}
		face := w.markdownFaces[el.style]
		if face == nil {
			continue
		}
		if el.style.code {
			g.DrawRect(clipped, Rect{x, y, el.w, el.h}, background)
		}
		c := g.theme.whiteColor
		if el.style.link || el.style.heading > 0 {
			c = g.theme.textColor
		}
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(x), float64(y))
		op.ColorScale.ScaleWithColor(c)
		text.Draw(clipped, el.text, face, op)
		metrics := face.Metrics()
		if el.style.link {
			g.DrawRect(clipped, Rect{x, y + float32(metrics.HAscent) + 1, el.w, 1}, c)
		}
		if el.style.strike {
			g.DrawRect(clipped, Rect{x, y + float32(metrics.HAscent)*0.65, el.w, 1}, c)
		}
	}
}

func blendMarkdownColor(a, b color.RGBA, amount float32) color.RGBA {
	mix := func(x, y uint8) uint8 { return uint8(float32(x)*(1-amount) + float32(y)*amount) }
	return color.RGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: 255}
}
