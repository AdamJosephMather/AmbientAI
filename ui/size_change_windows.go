package ui

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func nativeResizeInProgress() bool {
	// Ebitengine's mouse input does not cover dragging the native window border.
	// Query Windows' move/size loop, which also covers keyboard-driven resizing.
	var info windows.GUIThreadInfo
	info.Size = uint32(unsafe.Sizeof(info))
	if err := windows.GetGUIThreadInfo(0, &info); err != nil || info.MoveSize == 0 {
		return false
	}
	var pid uint32
	_, err := windows.GetWindowThreadProcessId(info.MoveSize, &pid)
	return err == nil && pid == windows.GetCurrentProcessId()
}
