package ui

import "time"

// Allow fullscreen, maximize, snap and display changes to finish delivering sizes.
const sizeChangeSettleDelay = 150 * time.Millisecond

type sizeChangeNotifier struct {
	initialized   bool
	width, height int
	pending       bool
	lastChange    time.Time
}

// update reports each settled size once. Native resizing must finish even if the
// user holds the border still longer than the settle delay.
func (n *sizeChangeNotifier) update(width, height int, now time.Time, resizing bool) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	if !n.initialized {
		n.initialized = true
		n.width, n.height = width, height
		n.pending = resizing
		n.lastChange = now
		return !resizing // Preserve the initial workspace notification.
	}
	if n.width != width || n.height != height {
		n.width, n.height = width, height
		n.pending = true
		n.lastChange = now
	}
	if !n.pending || resizing || now.Sub(n.lastChange) < sizeChangeSettleDelay {
		return false
	}
	n.pending = false
	return true
}
