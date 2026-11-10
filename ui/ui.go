package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math/rand/v2"
	"os"
	"strings"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
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
	GraphicSleep                    // 2
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
	kind elementKind
	style markdownStyle
	w, h float32
	ink elementInk
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
	laidOutBody string
	markdownFaces map[markdownStyle]*text.GoTextFace
	
	closeButton Button
	
	scroll float32
}

type MicLine struct {
	val float32
	status MicStatus
}

type Game struct {
	faceSource *text.GoTextFaceSource
	face       *text.GoTextFace
	markdownSources [8]*text.GoTextFaceSource
	
	setup bool
	lastId int
	interaction InteractionInfo
	
	textOffset float32
	edgeWidth  float32
	rectWidth float32
	workersCount int
	
	theme Theme
	
	width, height int
	oldwidth, oldheight int
	startwidth, startheight int
	
	oldMx, oldMy float32
	
	minWinWidth, minWinHeight float32
	stripSize, textHeight float32
	
	windows []Window
	buttonToWindow map[int]int
	
	micLines []MicLine
	MicLineCount int
	
	sleeping bool
	SleepDone func()
	sleepButton Button
	
	costMu sync.Mutex
	workerCost float64
	frontCost float64
}

type MicStatus int
const (
	MicStatus404          MicStatus = iota
	MicStatusListening
	MicStatusThinking
	MicStatusSpeaking
	MicStatusSleeping
)

func (g *Game) UpdateListening(val float32, status MicStatus) {
	mcLine := MicLine{
		val,
		status,
	}
	
	if len(g.micLines) < g.MicLineCount {
		g.micLines = append(g.micLines, mcLine)
	}else{
		for i := range(len(g.micLines)-1) {
			g.micLines[i] = g.micLines[i+1]
		}
		g.micLines[len(g.micLines)-1] = mcLine
	}
}

func (g *Game) AddFrontEndCost(cost float64) {
	g.costMu.Lock()
	defer g.costMu.Unlock()
	g.frontCost += cost
}

func (g *Game) AddWorkerCost(cost float64) {
	g.costMu.Lock()
	defer g.costMu.Unlock()
	g.workerCost += cost
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
	if id == g.sleepButton.id && g.SleepDone != nil {
		g.SleepDone()
		return
	}
	
	wId, exists := g.buttonToWindow[id]
	
	if exists {
		g.CloseWindow(wId)
		return
	}
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
		
		// Rebuild Markdown parts when content, width, or font settings change.
		
		g.layoutWindowMarkdown(win)
	}
	
	// Scroll the text in a window
	
	_, yoff := ebiten.Wheel()
	
	if g.interaction.interactabletype == InteractableWindow && g.interaction.interactiontype == InteractionHover {
		wIdx := g.FindWindow(g.interaction.id)
		if wIdx != -1 {
			w := &g.windows[wIdx]
			if overflow := w.scrollOverflow(g.stripSize); overflow > 0 {
				w.scroll = max(float32(0), min(float32(1), w.scroll-float32(yoff*3)*g.textHeight/overflow))
			} else {
				w.scroll = 0
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
	
	// position sleep button
	
	g.sleepButton.rect.x = float32(g.width)/2 - 5*g.textHeight
	g.sleepButton.rect.y = float32(g.height)/2 - 3*g.textHeight
	g.sleepButton.rect.w = 10*g.textHeight
	g.sleepButton.rect.h = 6*g.textHeight
	
	if g.sleeping && CoordIn(mX, mY, g.sleepButton.rect) && doHoverCheck {
		g.interaction.id = g.sleepButton.id
		g.interaction.hitArea = g.sleepButton.rect
		g.interaction.interactabletype = InteractableButton
		g.interaction.interactiontype = InteractionHover
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
	}else if graphic == GraphicSleep {
		w, h := text.Measure("Sleeping", g.face, 0)
		x := r.x+r.w/2 - float32(w)/2
		y := r.y+r.h/2 - float32(h)/2
		g.DrawTextLine(screen, x, y, r, "Sleeping", g.theme.textColor)
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

func (g *Game) DrawMicLines(screen *ebiten.Image) {
	offset := g.stripSize*2
	lineW := g.textHeight/5
	lineH := g.textHeight*2
	
	fullRect := Rect{
		-2*g.stripSize,
		-2*g.stripSize,
		lineW*float32(len(g.micLines))*2+offset*2+g.stripSize*2,
		lineH+2*offset+g.stripSize*2,
	}
	
	g.DrawBorder(screen, fullRect, g.stripSize, g.theme.borderColor)
	
	for i,val := range(g.micLines) {
		rect := Rect{
			float32(i)*(2*lineW)+offset,
			offset+(lineH-val.val*lineH)/2,
			lineW,
			val.val*lineH,
		}
		
		if (val.status == MicStatusListening) {
			g.DrawRect(screen, rect, g.theme.listeningColor)
		}else if (val.status == MicStatusThinking) {
			g.DrawRect(screen, rect, g.theme.thinkingColor)
		}else if (val.status == MicStatusSpeaking) {
			g.DrawRect(screen, rect, g.theme.speakingColor)
		}else if (val.status == MicStatusSleeping) {
			g.DrawRect(screen, rect, g.theme.sleepingColor)
		}
	}
}

func (g *Game) WorkerStarted() {
	g.workersCount += 1
}

func (g *Game) WorkerFinished() {
	g.workersCount -= 1
}

func (g *Game) SetSleep(sleep bool) {
	g.sleeping = sleep
}

func (g *Game) DrawWorkers(screen *ebiten.Image) {
	workerText := fmt.Sprintf("Workers: %d", g.workersCount)
	width, height := text.Measure(workerText, g.face, 0)
	boxWidth := float32(width)+g.stripSize*3
	boxHeight := float32(height)+g.stripSize*2
	leftEdge := float32(g.width) - boxWidth
	
	fullRect := Rect{
		leftEdge,
		-2*g.stripSize,
		boxWidth+2*g.stripSize,
		boxHeight+2*g.stripSize,
	}
	
	g.DrawBorder(screen, fullRect, g.stripSize, g.theme.borderColor)
	g.DrawTextLine(screen, leftEdge+g.stripSize*2, g.stripSize, fullRect, workerText, g.theme.textColor)
	
	// Cost
	
	g.costMu.Lock()
	costText := fmt.Sprintf("Workers: $%.3f\nFront End: $%.3f\nTotal: $%.3f", g.workerCost, g.frontCost, g.workerCost+g.frontCost)
	g.costMu.Unlock()
	width, height = text.Measure(costText, g.face, 0)
	boxWidth = float32(width)+g.stripSize*3
	boxHeight = float32(height)*3+g.stripSize*2 // measure only returns height for 1 line
	leftEdge = float32(g.width) - boxWidth
	
	fullRect = Rect{
		leftEdge,
		fullRect.y+fullRect.h+g.stripSize,
		boxWidth+2*g.stripSize,
		boxHeight,
	}
	
	g.DrawBorder(screen, fullRect, g.stripSize, g.theme.borderColor)
	for i, line := range(strings.Split(costText, "\n")) {
		g.DrawTextLine(screen, leftEdge+g.stripSize*2, g.stripSize*2+fullRect.y+float32(height)*float32(i), fullRect, line, g.theme.textColor)
	}
}

func (g *Game) Draw(screen *ebiten.Image) {
	fullRect := Rect{0, 0, float32(g.width), float32(g.height)}
	g.DrawBackground(screen, fullRect, g.stripSize/2, g.rectWidth, g.theme.background, g.theme.backgroundLines)
	g.DrawMicLines(screen)
	g.DrawWorkers(screen)
	
	for i := range(g.windows) {
		win := &g.windows[i]
		inset := win.rect
		inset.Inset(g.stripSize)
		g.DrawRect(screen, inset, g.theme.windowBackground)
		g.DrawTitleBar(screen, win)
		g.DrawElements(screen, win)
		g.DrawBorder(screen, win.rect, g.stripSize, g.theme.borderColor)
	}
	
	if g.sleeping {
		g.DrawButton(screen, g.sleepButton)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	g.width = int(float64(outsideWidth) * s)
	g.height = int(float64(outsideHeight) * s)
	return g.width, g.height
}

func (g *Game) SetupTheme() {
	g.theme.background       = color.RGBA{R: 46,  G: 13,  B: 8,   A: 255}
	g.theme.backgroundLines  = color.RGBA{R: 56,  G: 15,  B: 9,   A: 255}
	g.theme.windowBackground = color.RGBA{R: 46,  G: 13,  B: 8,   A: 255}
	g.theme.titleBackground  = color.RGBA{R: 122, G: 40,  B: 10,  A: 255}
	g.theme.borderColor      = color.RGBA{R: 231, G: 164, B: 74,  A: 255}
	g.theme.whiteColor       = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	g.theme.textColor        = color.RGBA{R: 253, G: 232, B: 172, A: 255}
	g.theme.buttonHovering   = color.RGBA{R: 142, G: 60,  B: 30,  A: 255}
	g.theme.buttonPressing   = color.RGBA{R: 102, G: 20,  B: 0,   A: 255}
	g.theme.listeningColor   = color.RGBA{R: 255, G: 190, B: 100, A: 255}
	g.theme.thinkingColor    = color.RGBA{R: 100, G: 255, B: 100, A: 255}
	g.theme.speakingColor    = color.RGBA{R: 100, G: 100, B: 255, A: 255}
	g.theme.sleepingColor    = color.RGBA{R: 100, G: 100, B: 100, A: 255}
	g.theme.transparent      = color.RGBA{R: 0,   G: 0,   B: 0,   A: 0  }
}

func (g *Game) SetupText(size int) error {
	if err := g.setupMarkdownFonts(size); err != nil {
		return err
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

func (g *Game) SetupAnim() {
	g.MicLineCount = 24
	g.micLines = make([]MicLine, 0)
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

func loadIcon(size int) ([]image.Image, error) {
	out := make([]image.Image, 0)
	
	for _,size := range([]string{"16x16", "32x32", "64x64", "128x128", "256x256"}) {
		f, err := os.Open(fmt.Sprintf("resources/icon_%s.png", size))
		
		if err != nil {
			return nil, err
		}
		
		defer f.Close()
		
		img, err := png.Decode(f)
		if err != nil {
			return nil, err
		}
		
		out = append(out, img)
	}
	
	return out, nil
}

func SetupUI(title string) (*Game, error) {
	os.Setenv("EBITENGINE_GRAPHICS_LIBRARY", "opengl")
	
	icons, err := loadIcon(256)
	if err != nil {
		log.Printf("Failed loading icon: %s", err.Error())
	}else{
		ebiten.SetWindowIcon(icons)
	}
	
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	ebiten.SetWindowSize(640, 480)
	ebiten.SetWindowTitle(title)
	ebiten.SetPreferredColorMode(ebiten.ColorModeDark)
	
	var game Game
	game.minWinHeight   = 100
	game.minWinWidth    = 100
	game.startwidth     = 640
	game.startheight    = 480
	game.buttonToWindow = make(map[int]int)
	game.SetupTheme()
	err = game.SetupText(17)
	if err != nil {
		return nil, err
	}
	game.SetupAnim()
	game.sleepButton = Button{
		game.GetNewId(),
		Rect{},
		GraphicSleep,
		game.theme.titleBackground,
		game.theme.buttonHovering,
		game.theme.buttonPressing,
	}
	
	return &game, nil
}

func (g* Game) RunUI() {
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}