//go:build !windows

package ui

import "github.com/hajimehoshi/ebiten/v2"

func nativeResizeInProgress() bool {
	return ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft)
}
