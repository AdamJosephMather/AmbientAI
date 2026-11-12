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
	X, Y, W, H float32
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

type WindowSimple struct {
	ID int
	
	Title string
	Body string
	
	Rect Rect
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
	UISizeChange func(width int, height int, newWindows []WindowSimple)
	UserResizedWindow func(newWin WindowSimple)
	UserClosedWindow func(id int)
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
	return x >= rect.X && y >= rect.Y && x <= rect.X+rect.W && y <= rect.Y+rect.H;
}

func (g *Game) PullItUp(id int) bool {
	swapping := false
	
	for i, w := range(g.windows) {
		if w.id == id {
			swapping = true
		}else if swapping {
			g.windows[i-1], g.windows[i] = g.windows[i], g.windows[i-1] // I love python
		}
	}
	
	return swapping
}

func (g *Game) FindWindow(id int) int {
	for i, w := range(g.windows) {
		if w.id == id {
			return i
		}
	}
	return -1
}

func (g *Game) CloseWindow(id int) bool {
	i := g.FindWindow(id)
	
	if i != -1 {
		g.windows = append(g.windows[:i], g.windows[i+1:]...) // removes the window
		return true
	}
	return false
}

func (g *Game) MoveWindowBy(dX, dY float32, id int) bool {
	i := g.FindWindow(id)
	
	if i != -1 {
		g.windows[i].rect.X += dX
		g.windows[i].rect.Y += dY
		return true
	}
	return false
}

func (g *Game) MoveWindowTo(id int, rect Rect) bool {
	i := g.FindWindow(id)
	
	if i != -1 {
		g.windows[i].rect = rect
		return true
	}
	
	return false
}

func (r *Rect) EnsureMinSize(minW, minH float32) {
	if r.W < minW {
		r.W = minW
	}
	if r.H < minH {
		r.H = minH
	}
}

func (g *Game) DragWindowEdgeBy(dX, dY float32, id int, edge Border) {
	i := g.FindWindow(id)
	
	if i == -1 {
		return
	}
	
	switch edge {
	case BorderBottom, BorderBottomLeft, BorderBottomRight:
		g.windows[i].rect.H += dY
	case BorderTop, BorderTopLeft, BorderTopRight:
		g.windows[i].rect.H -= dY
		g.windows[i].rect.Y += dY
	}
	switch edge {
	case BorderLeft, BorderTopLeft, BorderBottomLeft:
		g.windows[i].rect.W -= dX
		g.windows[i].rect.X += dX
	case BorderRight, BorderTopRight, BorderBottomRight:
		g.windows[i].rect.W += dX
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
		if g.UserClosedWindow != nil {
			go g.UserClosedWindow(id)
		}
		return
	}
}

func (g *Game) Update() error {
	// calculate positioning
	sendSizeUpdate := !g.setup // if we're not yet setup, send this
	
	if g.setup && (g.oldwidth != g.width || g.oldheight != g.height) { // if not setup, initial width might have been 0 (leading to inf)
		sendSizeUpdate = true
		
		floatOldWidth := float32(g.oldwidth)
		floatOldHeight := float32(g.oldheight)
		floatNewWidth := float32(g.width)
		floatNewHeight := float32(g.height)
		
		for i := range(g.windows) {
			g.windows[i].rect.X = floatNewWidth*float32(g.windows[i].rect.X)/floatOldWidth
			g.windows[i].rect.Y = floatNewHeight*float32(g.windows[i].rect.Y)/floatOldHeight
			g.windows[i].rect.W = floatNewWidth*float32(g.windows[i].rect.W)/floatOldWidth
			g.windows[i].rect.H = floatNewHeight*float32(g.windows[i].rect.H)/floatOldHeight
			
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
			
			switch iType {
			case InteractableTitleBar, InteractableWindow, InteractableWindowEdge:
				g.PullItUp(id)
			case InteractableButton:
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
			}else if (g.interaction.interactabletype == InteractableTitleBar || g.interaction.interactabletype == InteractableWindowEdge) {
				if g.UserResizedWindow != nil {
					w_idx := g.FindWindow(g.interaction.id)
					if w_idx != -1 {
						g.UserResizedWindow(WindowSimple{
							ID: g.windows[w_idx].id,
							Title: g.windows[w_idx].title,
							Body: g.windows[w_idx].body,
							Rect: g.windows[w_idx].rect,
						})
					}
				}
			}
		}
		g.interaction = InteractionInfo{}
	}else if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		if g.interaction.interactiontype == InteractionPress {
			switch g.interaction.interactabletype {
			case InteractableTitleBar:
				g.MoveWindowBy(dMX, dMY, g.interaction.id)
			case InteractableWindowEdge:
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
		
		expandedRect := Rect{win.rect.X - g.edgeWidth, win.rect.Y - g.edgeWidth, win.rect.W + 2*g.edgeWidth, win.rect.H + 2*g.edgeWidth}
		
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
			
			if mX <= win.rect.X { // left side
				if mY <= win.rect.Y { // top side
					interactionInfo.border = BorderTopLeft
				}else if mY >= win.rect.Y+win.rect.H { // bottom side
					interactionInfo.border = BorderBottomLeft
				}else { // left middle
					interactionInfo.border = BorderLeft
				}
			}else if mX >= win.rect.X+win.rect.W { // right side
				if mY <= win.rect.Y { // right top
					interactionInfo.border = BorderTopRight
				}else if mY >= win.rect.Y+win.rect.H { // right bottom
					interactionInfo.border = BorderBottomRight
				}else { // right middle
					interactionInfo.border = BorderRight
				}
			}else{
				if mY <= win.rect.Y { // top
					interactionInfo.border = BorderTop
				}else {
					interactionInfo.border = BorderBottom
				}
			}
			
			// this means it's in the edges
		}
		
		win.titleBarStrip = win.rect.Y+titleBarHeight+g.stripSize
		
		titlerect := Rect{win.rect.X, win.rect.Y, win.rect.W, titleBarHeight+g.stripSize*2}
		
		if doHoverCheck && CoordIn(mX, mY, titlerect) {
			interactionInfo.interactabletype = InteractableTitleBar
			interactionInfo.interactiontype  = InteractionHover
			interactionInfo.id = win.id
			interactionInfo.hitArea = titlerect
		}
		
		buttonWidth := graphicCloseWidth+2*g.stripSize
		
		win.closeButton.rect.X = win.rect.X+win.rect.W-g.stripSize-buttonWidth
		win.closeButton.rect.Y = win.rect.Y+g.stripSize
		win.closeButton.rect.W = buttonWidth
		win.closeButton.rect.H = titleBarHeight
		
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
	
	g.sleepButton.rect.X = float32(g.width)/2 - 5*g.textHeight
	g.sleepButton.rect.Y = float32(g.height)/2 - 3*g.textHeight
	g.sleepButton.rect.W = 10*g.textHeight
	g.sleepButton.rect.H = 6*g.textHeight
	
	if g.sleeping && CoordIn(mX, mY, g.sleepButton.rect) && doHoverCheck {
		g.interaction.id = g.sleepButton.id
		g.interaction.hitArea = g.sleepButton.rect
		g.interaction.interactabletype = InteractableButton
		g.interaction.interactiontype = InteractionHover
	}
	
	if g.interaction.interactabletype == InteractableWindowEdge {
		switch g.interaction.border {
		case BorderBottom, BorderTop:
			ebiten.SetCursorShape(ebiten.CursorShapeNSResize)
		case BorderLeft, BorderRight:
			ebiten.SetCursorShape(ebiten.CursorShapeEWResize)
		case BorderTopLeft, BorderBottomRight:
			ebiten.SetCursorShape(ebiten.CursorShapeNWSEResize)
		case BorderTopRight, BorderBottomLeft:
			ebiten.SetCursorShape(ebiten.CursorShapeNESWResize)
		}
	}else{
		ebiten.SetCursorShape(ebiten.CursorShapeDefault)
	}
	
	if sendSizeUpdate && g.UISizeChange != nil{
		list := make([]WindowSimple, len(g.windows))
		for i,w := range(g.windows) {
			list[i] = WindowSimple{
				ID: w.id,
				Title: w.title,
				Body: w.body,
				Rect: w.rect,
			}
		}
		
		go g.UISizeChange(g.width, g.height, list)
	}
	
	return nil
}

func (g *Game) DrawStripes(screen *ebiten.Image, rect Rect, stripSize float32, c1, c2 color.RGBA) {
	g.DrawRect(screen, rect, c1)
	
	strips := int(rect.H / g.stripSize / 2.0)
	for i := range(strips) {
		y := rect.Y + float32(i) * g.stripSize*2.0
		
		sRect := Rect{rect.X, y, rect.W, g.stripSize}
		g.DrawRect(screen, sRect, c2)
	}
}

func (g *Game) DrawBackground(screen *ebiten.Image, rect Rect, stripSize, rectWidth float32, c1, c2 color.RGBA) {
	g.DrawRect(screen, rect, c1)
	
	strips := int(rect.W / g.rectWidth) + 1
	
	for i := range(strips) {
		x := rect.X + float32(i) * g.rectWidth
		
		sRect := Rect{x, 0, stripSize, rect.H}
		g.DrawRect(screen, sRect, c2)
	}
	
	strips = int(rect.H / g.rectWidth) + 1
	
	for i := range(strips) {
		y := rect.Y + float32(i) * g.rectWidth
		
		sRect := Rect{0, y, rect.W, stripSize}
		g.DrawRect(screen, sRect, c2)
	}
}

func (g *Game) DrawBorder(screen *ebiten.Image, rect Rect, strip float32, c color.RGBA) {
	if strip < 0 {
		strip = 1
	}
	
	g.DrawRect(screen, Rect{rect.X+strip*2, rect.Y, rect.W-4*strip, strip}, c)
	g.DrawRect(screen, Rect{rect.X, rect.Y+strip*2, strip, rect.H-4*strip}, c)
	g.DrawRect(screen, Rect{rect.X+strip*2, rect.Y+rect.H-strip, rect.W-4*strip, strip}, c)
	g.DrawRect(screen, Rect{rect.X+rect.W-strip, rect.Y+strip*2, strip, rect.H-4*strip}, c)
	
	g.DrawRect(screen, Rect{rect.X+strip, rect.Y+strip, strip, strip}, c)
	g.DrawRect(screen, Rect{rect.X+rect.W-2*strip, rect.Y+strip, strip, strip}, c)
	g.DrawRect(screen, Rect{rect.X+strip, rect.Y+rect.H-2*strip, strip, strip}, c)
	g.DrawRect(screen, Rect{rect.X+rect.W-2*strip, rect.Y+rect.H-2*strip, strip, strip}, c)
}

func (g *Game) DrawGraphic(screen *ebiten.Image, r Rect, graphic Graphic) {
	r.Inset(g.stripSize)
	
	strokeWidth := r.W/8
	switch graphic {
	case GraphicClose:
		vector.StrokeLine(screen, r.X, r.Y, r.X+r.W, r.Y+r.H, strokeWidth, g.theme.borderColor, true)
		vector.StrokeLine(screen, r.X+r.W, r.Y, r.X, r.Y+r.H, strokeWidth, g.theme.borderColor, true)
	case GraphicSleep:
		w, h := text.Measure("Sleeping", g.face, 0)
		x := r.X+r.W/2 - float32(w)/2
		y := r.Y+r.H/2 - float32(h)/2
		g.DrawTextLine(screen, x, y, r, "Sleeping", g.theme.textColor)
	}
}

func (g *Game) DrawRect(screen *ebiten.Image, rect Rect, c color.RGBA) {
	vector.FillRect(screen, rect.X, rect.Y, rect.W, rect.H, c, false) // top bar
}

func (r *Rect) Inset(amount float32) {
	r.X += amount
	r.Y += amount
	r.W -= 2*amount
	r.H -= 2*amount
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
	titleRect := Rect{w.rect.X+g.stripSize, w.rect.Y+g.stripSize, w.rect.W-2*g.stripSize, w.titleBarStrip-w.rect.Y-g.stripSize}
	g.DrawRect(screen, titleRect, g.theme.titleBackground)
	vector.FillRect(screen, w.rect.X, w.titleBarStrip, w.rect.W, g.stripSize, g.theme.borderColor, false) // top bar
	g.DrawButton(screen, w.closeButton)
	maxWidth := w.rect.W-3*g.stripSize-w.closeButton.rect.W
	
	textX := w.rect.X+g.stripSize*2
	textY := w.rect.Y+g.stripSize*2
	
	rectDrawIn := Rect{textX, textY, maxWidth, w.closeButton.rect.H}
	g.DrawTextLine(screen, textX, textY, rectDrawIn, w.title, g.theme.textColor)
}

func (g *Game) DrawTextLine(screen *ebiten.Image, x, y float32, maxR Rect, todraw string, c color.RGBA) {
	y -= g.textOffset
	
	clipRect := image.Rect(
		int(maxR.X),
		int(maxR.Y),
		int(maxR.W+maxR.X),
		int(maxR.H+maxR.Y),
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
		
		switch val.status {
		case MicStatusListening:
			g.DrawRect(screen, rect, g.theme.listeningColor)
		case MicStatusThinking:
			g.DrawRect(screen, rect, g.theme.thinkingColor)
		case MicStatusSpeaking:
			g.DrawRect(screen, rect, g.theme.speakingColor)
		case MicStatusSleeping:
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
		fullRect.Y+fullRect.H+g.stripSize,
		boxWidth+2*g.stripSize,
		boxHeight,
	}
	
	g.DrawBorder(screen, fullRect, g.stripSize, g.theme.borderColor)
	for i, line := range(strings.Split(costText, "\n")) {
		g.DrawTextLine(screen, leftEdge+g.stripSize*2, g.stripSize*2+fullRect.Y+float32(height)*float32(i), fullRect, line, g.theme.textColor)
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

func (g *Game) AddWindow(title, body string, rect Rect) int {
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
	return win.id
}

func (g *Game) ChooseRect() Rect {
	var r Rect
	r.W = 300
	r.H = 300
	
	rangeX := g.width-int(r.W)
	rangeY := g.height-int(r.H)
	
	if g.width == 0 {
		rangeX = g.startwidth-int(r.W)
		rangeY = g.startheight-int(r.H)
	}
	if (rangeX < 1){
		rangeX = 1
	}
	if (rangeY < 1){
		rangeY = 1
	}
	
	r.X = float32(rand.IntN(rangeX))
	r.Y = float32(rand.IntN(rangeY))
	
	return r
}

func loadIcon() ([]image.Image, error) {
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
	
	icons, err := loadIcon()
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