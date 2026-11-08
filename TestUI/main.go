package main

import (
	"bytes"
	"image"
	"image/color"
	"log"
	"unicode/utf8"
	"math/rand/v2"

	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/goregular"
)

type Theme struct {
	windowBackground    color.RGBA
	titleBackground    color.RGBA
	
	background          color.RGBA
	backgroundLines          color.RGBA
	
	borderColor    color.RGBA
	textColor      color.RGBA
	whiteColor     color.RGBA
	buttonHovering color.RGBA
	buttonPressing color.RGBA
	
	listeningColor            color.RGBA
	thinkingColor             color.RGBA
	speakingColor             color.RGBA
	sleepingColor             color.RGBA
	
	transparent               color.RGBA
}

type Graphic int
const (
	Graphic404       Graphic = iota // 0 (Go's default zero-value)
	GraphicClose                    // 1
	GraphicMic                      // 2
)

type Interactable int
const (
	Interactable404       Interactable = iota // 0 (Go's default zero-value)
	InteractableButton                        // 1
	InteractableTitleBar                      // 2
	InteractableWindow                        // 3
	InteractableWindowEdge                    // 4
)

type Interaction int
const (
	Interaction404       Interaction = iota // 0 (Go's default zero-value)
	InteractionHover                        // 1
	InteractionPress                        // 2
)

type Rect struct {
	x, y, w, h float32
}

type Border int
const (
	Border404    Border = iota
	BorderLeft
	BorderRight
	BorderTop
	BorderBottom
	
	BorderTopLeft
	BorderTopRight
	BorderBottomLeft
	BorderBottomRight
)

type InteractionInfo struct {
	interactabletype Interactable
	interactiontype Interaction
	id int
	hitArea Rect
	
	// Specific to interaction (fatstruct?)
	border Border
}

type Button struct {
	id int
	rect Rect
	graphic Graphic
	background color.RGBA
	backgroundHover color.RGBA
	backgroundPress color.RGBA
}

type Element struct {
	yoffset float32
	xoffset float32
	text string
}

type Window struct {
	id int
	
	title string
	body string
	
	rect Rect
	titleBarStrip float32
	
	elements []Element
	oldwidth float32
	bodyheight float32
	
	closeButton Button
	
	scroll float32
}

type Game struct {
	faceSource *text.GoTextFaceSource
	face       *text.GoTextFace
	
	setup bool
	lastId int
	interaction InteractionInfo
	
	textOffset float32
	edgeWidth  float32
	rectWidth float32
	
	theme Theme
	
	width, height int
	oldwidth, oldheight int
	startwidth, startheight int
	
	oldMx, oldMy float32
	
	minWinWidth, minWinHeight float32
	stripSize, textHeight float32
	
	windows []Window
	buttonToWindow map[int]int
	
	buddyBackground *ebiten.Image
	buddyEyes       *ebiten.Image
	buddyEyesClosed *ebiten.Image
	buddySize       float32
	buddyEyeWidth   float32
	buddyEyeHeight  float32
	timeTillClosed  int
	timeTillEyesAtTarget int
	eyesX float32
	eyesY float32
	eyesTargetX float32
	eyesTargetY float32
}

func CoordIn(x, y float32, rect Rect) bool {
	return x >= rect.x && y >= rect.y && x <= rect.x+rect.w && y <= rect.y+rect.h;
}

func (g *Game) PullItUp(id int) {
	swapping := false
	
	for i, w := range(g.windows) {
		if w.id == id {
			swapping = true
		}else if swapping {
			g.windows[i-1], g.windows[i] = g.windows[i], g.windows[i-1] // I love python
		}
	}
}

func (g *Game) FindWindow(id int) int {
	for i, w := range(g.windows) {
		if w.id == id {
			return i
		}
	}
	return -1
}

func (g *Game) CloseWindow(id int) {
	i := g.FindWindow(id)
	
	if i != -1 {
		g.windows = append(g.windows[:i], g.windows[i+1:]...) // removes the window
	}
}

func (g *Game) MoveWindowBy(dX, dY float32, id int) {
	i := g.FindWindow(id)
	
	if i != -1 {
		g.windows[i].rect.x += dX
		g.windows[i].rect.y += dY
	}
}

func (r *Rect) EnsureMinSize(minW, minH float32) {
	if r.w < minW {
		r.w = minW
	}
	if r.h < minH {
		r.h = minH
	}
}

func (g *Game) DragWindowEdgeBy(dX, dY float32, id int, edge Border) {
	i := g.FindWindow(id)
	
	if i == -1 {
		return
	}
	
	if (edge == BorderBottom || edge == BorderBottomLeft ||  edge == BorderBottomRight) {
		g.windows[i].rect.h += dY
	}else if (edge == BorderTop || edge == BorderTopLeft ||  edge == BorderTopRight) {
		g.windows[i].rect.h -= dY
		g.windows[i].rect.y += dY
	}
	if (edge == BorderLeft || edge == BorderTopLeft ||  edge == BorderBottomLeft) {
		g.windows[i].rect.w -= dX
		g.windows[i].rect.x += dX
	}else if (edge == BorderRight || edge == BorderTopRight ||  edge == BorderBottomRight) {
		g.windows[i].rect.w += dX
	}
	
	g.windows[i].rect.EnsureMinSize(g.minWinWidth, g.minWinHeight)
}

func (g *Game) DoButton(id int) {
	wId, exists := g.buttonToWindow[id]
	
	if exists {
		g.CloseWindow(wId)
	}
	
	// other button interactions here
}

func (g *Game) Update() error {
	// calculate positioning
	if g.setup { // if not setup, initial width might have been 0 (leading to inf)
		floatOldWidth := float32(g.oldwidth)
		floatOldHeight := float32(g.oldheight)
		floatNewWidth := float32(g.width)
		floatNewHeight := float32(g.height)
		
		for i := range(g.windows) {
			g.windows[i].rect.x = floatNewWidth*float32(g.windows[i].rect.x)/floatOldWidth
			g.windows[i].rect.y = floatNewHeight*float32(g.windows[i].rect.y)/floatOldHeight
			g.windows[i].rect.w = floatNewWidth*float32(g.windows[i].rect.w)/floatOldWidth
			g.windows[i].rect.h = floatNewHeight*float32(g.windows[i].rect.h)/floatOldHeight
			
			g.windows[i].rect.EnsureMinSize(g.minWinWidth, g.minWinHeight)
		}
	}
	
	// calculate text rendering && things that will not break with oldwinheight == 0
	
	iMX, iMY := ebiten.CursorPosition()
	mX := float32(iMX)
	mY := float32(iMY)
	
	dMX := mX-g.oldMx
	dMY := mY-g.oldMy
	
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		if (g.interaction.interactiontype == InteractionHover) {
			g.interaction.interactiontype = InteractionPress
			id := g.interaction.id
			iType := g.interaction.interactabletype
			
			if (iType == InteractableTitleBar || iType == InteractableWindow || iType == InteractableWindowEdge) {
				g.PullItUp(id)
			}else if (iType == InteractableButton) {
				val, exists := g.buttonToWindow[id]
				if exists {
					g.PullItUp(val)
				}
			}
		}
	}else if inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft) {
		if (g.interaction.interactiontype == InteractionPress) {
			if (g.interaction.interactabletype == InteractableButton && CoordIn(mX, mY, g.interaction.hitArea)) {
				g.DoButton(g.interaction.id)
			}
		}
		g.interaction = InteractionInfo{}
	}else if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		if g.interaction.interactiontype == InteractionPress {
			if g.interaction.interactabletype == InteractableTitleBar {
				g.MoveWindowBy(dMX, dMY, g.interaction.id)
			}else if g.interaction.interactabletype == InteractableWindowEdge {
				g.DragWindowEdgeBy(dMX, dMY, g.interaction.id, g.interaction.border)
			}
		}
	}
	
	doHoverCheck := g.interaction.interactiontype != InteractionPress
	var interactionInfo InteractionInfo
	
	titleBarHeight    := g.textHeight + g.stripSize * 2
	graphicCloseWidth := titleBarHeight-2*g.stripSize
	
	for i := range(g.windows) {
		win := &g.windows[i]
		
		expandedRect := Rect{win.rect.x - g.edgeWidth, win.rect.y - g.edgeWidth, win.rect.w + 2*g.edgeWidth, win.rect.h + 2*g.edgeWidth}
		
		if doHoverCheck && CoordIn(mX, mY, win.rect) {
			interactionInfo.interactabletype = InteractableWindow
			interactionInfo.interactiontype  = InteractionHover
			interactionInfo.id = win.id
			interactionInfo.hitArea = win.rect
		}else if (doHoverCheck && CoordIn(mX, mY, expandedRect)) {
			interactionInfo.interactabletype = InteractableWindowEdge
			interactionInfo.interactiontype  = InteractionHover
			interactionInfo.id = win.id
			interactionInfo.hitArea = expandedRect
			
			if mX <= win.rect.x { // left side
				if mY <= win.rect.y { // top side
					interactionInfo.border = BorderTopLeft
				}else if mY >= win.rect.y+win.rect.h { // bottom side
					interactionInfo.border = BorderBottomLeft
				}else { // left middle
					interactionInfo.border = BorderLeft
				}
			}else if mX >= win.rect.x+win.rect.w { // right side
				if mY <= win.rect.y { // right top
					interactionInfo.border = BorderTopRight
				}else if mY >= win.rect.y+win.rect.h { // right bottom
					interactionInfo.border = BorderBottomRight
				}else { // right middle
					interactionInfo.border = BorderRight
				}
			}else{
				if mY <= win.rect.y { // top
					interactionInfo.border = BorderTop
				}else {
					interactionInfo.border = BorderBottom
				}
			}
			
			// this means it's in the edges
		}
		
		win.titleBarStrip = win.rect.y+titleBarHeight+g.stripSize
		
		titlerect := Rect{win.rect.x, win.rect.y, win.rect.w, titleBarHeight+g.stripSize*2}
		
		if doHoverCheck && CoordIn(mX, mY, titlerect) {
			interactionInfo.interactabletype = InteractableTitleBar
			interactionInfo.interactiontype  = InteractionHover
			interactionInfo.id = win.id
			interactionInfo.hitArea = titlerect
		}
		
		buttonWidth := graphicCloseWidth+2*g.stripSize
		
		win.closeButton.rect.x = win.rect.x+win.rect.w-g.stripSize-buttonWidth
		win.closeButton.rect.y = win.rect.y+g.stripSize
		win.closeButton.rect.w = buttonWidth
		win.closeButton.rect.h = titleBarHeight
		
		if doHoverCheck && CoordIn(mX, mY, win.closeButton.rect) {
			interactionInfo.interactabletype = InteractableButton
			interactionInfo.interactiontype  = InteractionHover
			interactionInfo.id = win.closeButton.id
			interactionInfo.hitArea = win.closeButton.rect
		}
		
		// calculate window text given window width if window width has changed
		
		if win.oldwidth == win.rect.w {
			continue
		}
		
		win.elements = make([]Element, 0)
		
		maxWidth := float64(win.rect.w - 4*g.stripSize)
		lineStart := g.stripSize
		
		m := g.face.Metrics()
		lineHeight := float32(m.HAscent + m.HDescent + m.HLineGap)
		
		curY := g.stripSize
		curStart := 0
		
		// end: exclusive end of the line's text; next: where the next line begins
		flush := func(end, next int) {
			win.elements = append(win.elements, Element{curY, lineStart, win.body[curStart:end]})
			curStart = next
			curY += lineHeight
		}
		
		for i, r := range win.body {
			next := i + utf8.RuneLen(r)
		
			if r == '\n' {
				flush(i, next) // exclude the newline itself
				continue
			}
		
			width, _ := text.Measure(win.body[curStart:next], g.face, 0) // includes current rune
			if width > maxWidth {
				if i == curStart {
					flush(next, next) // single rune wider than the line; emit it anyway
				} else {
					flush(i, i) // current rune starts the next line
				}
			}
		}
		
		if curStart < len(win.body) { // remaining text after the last wrap/newline
			flush(len(win.body), len(win.body))
		}
		
		if curStart < len(win.body) {
			part := win.body[curStart:]
			_, height := text.Measure(part, g.face, 0) 
			el := Element{curY, lineStart, part}
			win.elements = append(win.elements, el)
			curY += float32(height)
		}
		
		win.bodyheight = curY+g.stripSize
		win.oldwidth = win.rect.w
	}
	
	// Scroll the text in a window
	
	_, yoff := ebiten.Wheel()
	
	if g.interaction.interactabletype == InteractableWindow && g.interaction.interactiontype == InteractionHover {
		wIdx := g.FindWindow(g.interaction.id)
		if wIdx != -1 {
			g.windows[wIdx].scroll -= float32(yoff*3) * g.textHeight/g.windows[wIdx].bodyheight
			if g.windows[wIdx].scroll < 0 {
				g.windows[wIdx].scroll = 0
			}else if g.windows[wIdx].scroll > 1 {
				g.windows[wIdx].scroll = 1
			}
		}
	}
	
	
	g.setup = true
	g.oldwidth = g.width
	g.oldheight = g.height
	g.oldMx = mX
	g.oldMy = mY
	if doHoverCheck {
		g.interaction = interactionInfo
	}
	
	if g.interaction.interactabletype == InteractableWindowEdge {
		if g.interaction.border == BorderBottom || g.interaction.border == BorderTop {
			ebiten.SetCursorShape(ebiten.CursorShapeNSResize)
		}else if g.interaction.border == BorderLeft || g.interaction.border == BorderRight {
			ebiten.SetCursorShape(ebiten.CursorShapeEWResize)
		}else if g.interaction.border == BorderTopLeft || g.interaction.border == BorderBottomRight {
			ebiten.SetCursorShape(ebiten.CursorShapeNWSEResize)
		}else if g.interaction.border == BorderTopRight || g.interaction.border == BorderBottomLeft {
			ebiten.SetCursorShape(ebiten.CursorShapeNESWResize)
		}
	}else{
		ebiten.SetCursorShape(ebiten.CursorShapeDefault)
	}
	
	g.timeTillClosed -= 1
	if g.timeTillClosed < -15 { // measured in frames
		g.timeTillClosed = 10*60+rand.IntN(6*60)
	}
	
	g.timeTillEyesAtTarget -= 1
	if g.timeTillEyesAtTarget <= 0 {
		g.eyesTargetX = float32(rand.IntN(int(g.buddyEyeWidth*2)))-g.buddyEyeWidth
		g.eyesTargetY = float32(rand.IntN(int(g.buddyEyeWidth*2)))-g.buddyEyeWidth
		g.timeTillEyesAtTarget = rand.IntN(3*60)+60
	}else {
		g.eyesX += (g.eyesTargetX-g.eyesX)/float32(g.timeTillEyesAtTarget)
		g.eyesY += (g.eyesTargetY-g.eyesY)/float32(g.timeTillEyesAtTarget)
	}
	
	return nil
}

func (g *Game) DrawStripes(screen *ebiten.Image, rect Rect, stripSize float32, c1, c2 color.RGBA) {
	g.DrawRect(screen, rect, c1)
	
	strips := int(rect.h / g.stripSize / 2.0)
	for i := range(strips) {
		y := rect.y + float32(i) * g.stripSize*2.0
		
		sRect := Rect{rect.x, y, rect.w, g.stripSize}
		g.DrawRect(screen, sRect, c2)
	}
}

func (g *Game) DrawBackground(screen *ebiten.Image, rect Rect, stripSize, rectWidth float32, c1, c2 color.RGBA) {
	g.DrawRect(screen, rect, c1)
	
	strips := int(rect.w / g.rectWidth) + 1
	
	for i := range(strips) {
		x := rect.x + float32(i) * g.rectWidth
		
		sRect := Rect{x, 0, stripSize, rect.h}
		g.DrawRect(screen, sRect, c2)
	}
	
	strips = int(rect.h / g.rectWidth) + 1
	
	for i := range(strips) {
		y := rect.y + float32(i) * g.rectWidth
		
		sRect := Rect{0, y, rect.w, stripSize}
		g.DrawRect(screen, sRect, c2)
	}
}

func (g *Game) DrawBorder(screen *ebiten.Image, rect Rect, strip float32, c color.RGBA) {
	if strip < 0 {
		strip = 1
	}
	
	g.DrawRect(screen, Rect{rect.x+strip*2, rect.y, rect.w-4*strip, strip}, c)
	g.DrawRect(screen, Rect{rect.x, rect.y+strip*2, strip, rect.h-4*strip}, c)
	g.DrawRect(screen, Rect{rect.x+strip*2, rect.y+rect.h-strip, rect.w-4*strip, strip}, c)
	g.DrawRect(screen, Rect{rect.x+rect.w-strip, rect.y+strip*2, strip, rect.h-4*strip}, c)
	
	g.DrawRect(screen, Rect{rect.x+strip, rect.y+strip, strip, strip}, c)
	g.DrawRect(screen, Rect{rect.x+rect.w-2*strip, rect.y+strip, strip, strip}, c)
	g.DrawRect(screen, Rect{rect.x+strip, rect.y+rect.h-2*strip, strip, strip}, c)
	g.DrawRect(screen, Rect{rect.x+rect.w-2*strip, rect.y+rect.h-2*strip, strip, strip}, c)
}

func (g *Game) DrawGraphic(screen *ebiten.Image, r Rect, graphic Graphic) {
	r.Inset(g.stripSize)
	
	strokeWidth := r.w/8
	if graphic == GraphicClose {
		vector.StrokeLine(screen, r.x, r.y, r.x+r.w, r.y+r.h, strokeWidth, g.theme.borderColor, true)
		vector.StrokeLine(screen, r.x+r.w, r.y, r.x, r.y+r.h, strokeWidth, g.theme.borderColor, true)
	}else if graphic == GraphicMic {
		
	}
}

func (g *Game) DrawRect(screen *ebiten.Image, rect Rect, c color.RGBA) {
	vector.FillRect(screen, rect.x, rect.y, rect.w, rect.h, c, false) // top bar
}

func (r *Rect) Inset(amount float32) {
	r.x += amount
	r.y += amount
	r.w -= 2*amount
	r.h -= 2*amount
}

func (g *Game) DrawButton(screen *ebiten.Image, button Button) {
	active := button.id == g.interaction.id
	borderSize := float32(int(g.stripSize/2))
	smallerRect := button.rect
	smallerRect.Inset(g.stripSize)
	smallestRect := smallerRect
	smallestRect.Inset(borderSize)
	
	if (active && g.interaction.interactiontype == InteractionHover) {
		g.DrawRect(screen, smallestRect, button.backgroundHover)
	}else if (active && g.interaction.interactiontype == InteractionPress) {
		g.DrawRect(screen, smallestRect, button.backgroundPress)
	}else{
		g.DrawRect(screen, smallestRect, button.background)
	}
	
	g.DrawBorder(screen, smallerRect, borderSize, g.theme.borderColor)
	
	smallerRect.Inset(1)
	
	g.DrawGraphic(screen, smallerRect, button.graphic)
}

func (g *Game) DrawElements(screen *ebiten.Image, w *Window) {
	offsetX := w.rect.x+g.stripSize
	offsetY := w.titleBarStrip + g.stripSize - w.scroll * w.bodyheight
	
	drawIn := Rect{w.rect.x, w.titleBarStrip + g.stripSize, w.rect.w, w.rect.h + w.rect.y - w.titleBarStrip - g.stripSize*2}
	
	for _, el := range(w.elements) {
		g.DrawTextLine(screen, offsetX+el.xoffset, offsetY+el.yoffset, drawIn, el.text, g.theme.whiteColor)
	}
}

func (g *Game) DrawTitleBar(screen *ebiten.Image, w *Window) {
	titleRect := Rect{w.rect.x+g.stripSize, w.rect.y+g.stripSize, w.rect.w-2*g.stripSize, w.titleBarStrip-w.rect.y-g.stripSize}
	g.DrawRect(screen, titleRect, g.theme.titleBackground)
	vector.FillRect(screen, w.rect.x, w.titleBarStrip, w.rect.w, g.stripSize, g.theme.borderColor, false) // top bar
	g.DrawButton(screen, w.closeButton)
	maxWidth := w.rect.w-3*g.stripSize-w.closeButton.rect.w
	
	textX := w.rect.x+g.stripSize*2
	textY := w.rect.y+g.stripSize*2
	
	rectDrawIn := Rect{textX, textY, maxWidth, w.closeButton.rect.h}
	g.DrawTextLine(screen, textX, textY, rectDrawIn, w.title, g.theme.textColor)
}

func (g *Game) DrawTextLine(screen *ebiten.Image, x, y float32, maxR Rect, todraw string, c color.RGBA) {
	y -= g.textOffset
	
	clipRect := image.Rect(
		int(maxR.x),
		int(maxR.y),
		int(maxR.w+maxR.x),
		int(maxR.h+maxR.y),
	)

	clippedScreen := screen.SubImage(clipRect).(*ebiten.Image)

	op := &text.DrawOptions{}
	op.GeoM.Translate(float64(x), float64(y))
	op.ColorScale.ScaleWithColor(c)

	text.Draw(clippedScreen, todraw, g.face, op)
}

func (g *Game) DrawBuddy(screen *ebiten.Image) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Scale(1, 1)
	op.GeoM.Translate(20, 20)
	screen.DrawImage(g.buddyBackground, op)
	
	centerX := 20+g.buddySize/2
	eyeY := 20+g.buddySize/2-g.buddyEyeHeight/2
	
	op = &ebiten.DrawImageOptions{}
	op.GeoM.Scale(1, 1)
	op.GeoM.Translate(float64(centerX - g.buddyEyeWidth * 1.5 + g.eyesX), float64(eyeY + g.eyesY))
	if g.timeTillClosed <= 0 {
		screen.DrawImage(g.buddyEyesClosed, op)
	}else{
		screen.DrawImage(g.buddyEyes, op)
	}
	
	op = &ebiten.DrawImageOptions{}
	op.GeoM.Scale(1, 1)
	op.GeoM.Translate(float64(centerX + g.buddyEyeWidth * 0.5 + g.eyesX), float64(eyeY + g.eyesY))
	if g.timeTillClosed <= 0 {
		screen.DrawImage(g.buddyEyesClosed, op)
	}else{
		screen.DrawImage(g.buddyEyes, op)
	}
}

func (g *Game) Draw(screen *ebiten.Image) {
	fullRect := Rect{0, 0, float32(g.width), float32(g.height)}
	g.DrawBackground(screen, fullRect, g.stripSize/2, g.rectWidth, g.theme.background, g.theme.backgroundLines)
	g.DrawBuddy(screen)
	
	for i := range(g.windows) {
		win := &g.windows[i]
		inset := win.rect
		inset.Inset(g.stripSize)
		g.DrawRect(screen, inset, g.theme.windowBackground)
		g.DrawTitleBar(screen, win)
		g.DrawElements(screen, win)
		g.DrawBorder(screen, win.rect, g.stripSize, g.theme.borderColor)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	g.width = outsideWidth
	g.height = outsideHeight
	return outsideWidth, outsideHeight
}

func (g *Game) SetupTheme() {
	g.theme.background       = color.RGBA{R: 46,  G: 13,  B: 8,   A: 255}
	g.theme.backgroundLines  = color.RGBA{R: 60,  G: 17,  B: 5,   A: 255}
	g.theme.windowBackground = color.RGBA{R: 46,  G: 13,  B: 8,   A: 255}
	g.theme.titleBackground  = color.RGBA{R: 122, G: 40,  B: 10,  A: 255}
	g.theme.borderColor      = color.RGBA{R: 231, G: 164, B: 74,  A: 255}
	g.theme.whiteColor       = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	g.theme.textColor        = color.RGBA{R: 253, G: 232, B: 172, A: 255}
	g.theme.buttonHovering   = color.RGBA{R: 142, G: 60,  B: 30,  A: 255}
	g.theme.buttonPressing   = color.RGBA{R: 102, G: 20,  B: 0,   A: 255}
	g.theme.listeningColor   = color.RGBA{R: 255, G: 100, B: 100, A: 255}
	g.theme.thinkingColor    = color.RGBA{R: 255, G: 180, B: 150, A: 255}
	g.theme.speakingColor    = color.RGBA{R: 100, G: 100, B: 255, A: 255}
	g.theme.sleepingColor    = color.RGBA{R: 100, G: 100, B: 100, A: 255}
	g.theme.transparent      = color.RGBA{R: 0,   G: 0,   B: 0,   A: 0  }
}

func (g *Game) SetupText(size int) error {
	s, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		return err
	}
	g.faceSource = s
	g.face = &text.GoTextFace{
		Source: g.faceSource,
		Size:   float64(size),
	}
	g.textHeight = float32(size)
	g.textOffset = float32(int(g.textHeight/10))
	g.edgeWidth  = float32(int(g.textHeight/2))
	g.rectWidth  = float32(int(g.textHeight*2.5))
	g.stripSize  = float32(int(g.textHeight/6))
	return nil
}

func (g *Game) DrawCircle(screen *ebiten.Image, x, y, r float32, c color.Color) {
	vector.FillCircle(screen, x, y, r, c, true)
}

func (g *Game) CreateBuddyBackground(radius float32) *ebiten.Image {
	canvas := ebiten.NewImage(int(radius*2), int(radius*2))
	canvas.Clear()
	
	g.buddySize      = radius*2
	g.buddyEyeWidth  = radius/5
	g.buddyEyeHeight = g.buddyEyeWidth*2

	const shaderSrc = `//kage:unit pixels
package main

var Center vec2
var Radius float
var Inner float // fraction of radius that stays solid, e.g. 0.8
var Color vec4  // straight (non-premultiplied) RGBA, 0..1

func Fragment(dstPos vec4, srcPos vec2, _ vec4) vec4 {
	d := distance(dstPos.xy, Center) / Radius
	a := (1 - smoothstep(Inner, 1.0, d)) * Color.a
	return vec4(Color.rgb*a, a) // output must be premultiplied
}
	`
	
	s, err := ebiten.NewShader([]byte(shaderSrc))
	if err != nil {
		log.Fatal(err)
	}
	
	size := int(radius * 2)
	op := &ebiten.DrawRectShaderOptions{
		Uniforms: map[string]any{
			"Center": []float32{radius, radius}, // in dst pixel coords
			"Radius": radius,
			"Inner":  float32(0.7),
			"Color":  []float32{255.0 / 255, 109.0 / 255, 20.0 / 255, 1},
		},
	}
	canvas.DrawRectShader(size, size, s, op)
	
	bright  := color.NRGBA{R: 255, G: 233, B: 114, A: 255}
	darker  := color.NRGBA{R: 251, G: 116, B: 8,  A: 255}
	darkest := color.NRGBA{R: 63,  G: 14,  B: 0,  A: 255}
	
	g.DrawCircle(canvas, radius, radius, radius*0.73, bright)
	g.DrawCircle(canvas, radius, radius, radius*0.66, darker)
	g.DrawCircle(canvas, radius, radius, radius*0.56, darkest)
	
	return canvas
}

func (g *Game) CreateBuddyEyes() *ebiten.Image {
	canvas := ebiten.NewImage(int(g.buddyEyeWidth*2), int(g.buddyEyeHeight*2))
	canvas.Clear()
	
	rad := g.buddyEyeWidth/2
	
	bright  := color.RGBA{R: 255, G: 233, B: 114, A: 255}
	darker  := color.RGBA{R: 251, G: 116, B: 8,  A: 255}
	
	g.DrawCircle(canvas, rad, rad, rad, darker)
	g.DrawCircle(canvas, rad, g.buddyEyeHeight-rad, rad, darker)
	g.DrawRect(canvas, Rect{0, rad, g.buddyEyeWidth, g.buddyEyeHeight-rad*2}, darker)
	
	smallRad := (rad*2)/3
	g.DrawCircle(canvas, rad, rad, smallRad, bright)
	g.DrawCircle(canvas, rad, g.buddyEyeHeight-rad, smallRad, bright)
	g.DrawRect(canvas, Rect{smallRad/2, rad, g.buddyEyeWidth-smallRad, g.buddyEyeHeight-rad*2}, bright)
	
	return canvas
}

func (g *Game) CreateBuddyEyesClosed() *ebiten.Image {
	canvas := ebiten.NewImage(int(g.buddyEyeWidth*2), int(g.buddyEyeHeight*2))
	canvas.Clear()
	
	rad := g.buddyEyeWidth/2
	
//	bright  := color.RGBA{R: 255, G: 233, B: 114, A: 255}
	darker  := color.RGBA{R: 251, G: 116, B: 8,  A: 255}
	darkest := color.RGBA{R: 63,  G: 14,  B: 0,  A: 255}
	
	g.DrawCircle(canvas, rad, g.buddyEyeHeight/2-rad/2, rad, darker)
	smallRad := (rad*2)/3
	g.DrawCircle(canvas, rad, g.buddyEyeHeight/2-rad/2, smallRad, darkest)
	
	g.DrawRect(canvas, Rect{0, 0, g.buddyEyeWidth, g.buddyEyeHeight/2-rad/2}, darkest)
	
	return canvas
}

func (g *Game) SetupAnim() {
	rad := g.textHeight*4
	
	g.buddyBackground = g.CreateBuddyBackground(rad)
	g.buddyEyes = g.CreateBuddyEyes()
	g.buddyEyesClosed = g.CreateBuddyEyesClosed()
	
//	eyes := GetBackground()
//	g.buddyEyes = g.ToCanvas(eyes, pixWidth)
	
//	eyesclosed := GetBackground()
//	g.buddyEyesClosed = g.ToCanvas(eyesclosed, pixWidth)
}

func (g *Game) GetNewId() int {
	g.lastId += 1
	return g.lastId
}

func (g *Game) AddWindow(title, body string, rect Rect) {
	var win Window
	win.id = g.GetNewId()
	win.closeButton.id = g.GetNewId()
	win.closeButton.graphic = GraphicClose
	win.closeButton.background = g.theme.transparent
	win.closeButton.backgroundHover = g.theme.buttonHovering
	win.closeButton.backgroundPress = g.theme.buttonPressing
	g.buttonToWindow[win.closeButton.id] = win.id
	win.rect  = rect
	win.title = title
	win.body  = body
	g.windows = append(g.windows, win)
}

func (g *Game) ChooseRect() Rect {
	var r Rect
	r.w = 300
	r.h = 300
	
	rangeX := g.width-int(r.w)
	rangeY := g.height-int(r.h)
	
	if g.width == 0 {
		rangeX = g.startwidth-int(r.w)
		rangeY = g.startheight-int(r.h)
	}
	if (rangeX < 1){
		rangeX = 1
	}
	if (rangeY < 1){
		rangeY = 1
	}
	
	r.x = float32(rand.IntN(rangeX))
	r.y = float32(rand.IntN(rangeY))
	
	return r
}

func main() {
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowSize(640, 480)
	ebiten.SetWindowTitle("Ebitengine Rectangle and Text Example")
	
	var game Game
	game.minWinHeight   = 100
	game.minWinWidth    = 100
	game.startwidth     = 640
	game.startheight    = 480
	game.buttonToWindow = make(map[int]int)
	game.SetupTheme()
	err := game.SetupText(15)
	if err != nil {
		log.Fatalf(err.Error())
		return
	}
	game.SetupAnim()
	
	game.AddWindow("Test title", "Test body text goes here. This is a long bit of text.\n\nAnd more text", game.ChooseRect())
	game.AddWindow("Second Test title", "More test text for the body goes here.\n\nLorem ipsum delor something", game.ChooseRect())
	
	if err := ebiten.RunGame(&game); err != nil {
		log.Fatal(err)
	}
}