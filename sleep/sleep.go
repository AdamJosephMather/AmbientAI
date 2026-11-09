// Package sleepctl keeps the screen on and the session from idle-locking.
//
// Implementation is stdlib-only and works by holding a helper process that
// owns the OS-level inhibit request:
//
//	macOS:   caffeinate -d -i -w <pid>
//	Linux:   gnome-session-inhibit, kde-inhibit, or systemd-inhibit (first that works)
//	Windows: powershell + SetThreadExecutionState (display + system required)
//
// Each helper exits on its own if this process dies, so the inhibit does not
// outlive your program.
package sleepctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type inhibitor struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (i *inhibitor) running() bool {
	select {
	case <-i.done:
		return false
	default:
		return true
	}
}

var (
	mu     sync.Mutex
	active *inhibitor
)

// SetCanSleep controls whether the screen / session may idle off.
//
//	SetCanSleep(false) - keep the screen on and prevent idle lock
//	SetCanSleep(true)  - restore normal behavior
//
// Calls are idempotent and safe for concurrent use. This does not stop a
// manual lock (Win+L, Ctrl+Cmd+Q), closing a laptop lid, or lock policies
// enforced by an administrator.
func SetCanSleep(canSleep bool) error {
	mu.Lock()
	defer mu.Unlock()

	if canSleep {
		if active != nil {
			_ = active.cmd.Process.Kill()
			active = nil
		}
		return nil
	}

	if active != nil && active.running() {
		return nil
	}
	active = nil

	candidates, err := buildCommands()
	if err != nil {
		return err
	}

	var lastErr error
	for _, cmd := range candidates {
		inh, err := start(cmd)
		if err != nil {
			lastErr = err
			continue
		}
		active = inh
		return nil
	}
	return fmt.Errorf("sleepctl: no inhibitor could be started: %w", lastErr)
}

// start launches cmd and treats an exit within a short window as failure
// (e.g. no session bus, unsupported desktop).
func start(cmd *exec.Cmd) (*inhibitor, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	inh := &inhibitor{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(inh.done)
	}()
	select {
	case <-inh.done:
		return nil, fmt.Errorf("%s exited immediately", cmd.Path)
	case <-time.After(400 * time.Millisecond):
		return inh, nil
	}
}

func buildCommands() ([]*exec.Cmd, error) {
	pid := strconv.Itoa(os.Getpid())

	switch runtime.GOOS {
	case "darwin":
		// -d: prevent display sleep, -i: prevent idle sleep, -w: exit with pid.
		return []*exec.Cmd{exec.Command("caffeinate", "-d", "-i", "-w", pid)}, nil

	case "linux":
		loop := "while kill -0 " + pid + " 2>/dev/null; do sleep 5; done"
		const reason = "Application requested screen stay on"
		var cmds []*exec.Cmd
		if _, err := exec.LookPath("gnome-session-inhibit"); err == nil {
			cmds = append(cmds, exec.Command("gnome-session-inhibit",
				"--inhibit", "idle", "--reason", reason, "sh", "-c", loop))
		}
		if _, err := exec.LookPath("kde-inhibit"); err == nil {
			cmds = append(cmds, exec.Command("kde-inhibit",
				"--screenSaver", "sh", "-c", loop))
		}
		if _, err := exec.LookPath("systemd-inhibit"); err == nil {
			cmds = append(cmds, exec.Command("systemd-inhibit",
				"--what=idle", "--who=sleepctl", "--why="+reason,
				"--mode=block", "sh", "-c", loop))
		}
		if len(cmds) == 0 {
			return nil, errors.New("sleepctl: none of gnome-session-inhibit, kde-inhibit, systemd-inhibit found")
		}
		return cmds, nil

	case "windows":
		// ES_CONTINUOUS (0x80000000) | ES_DISPLAY_REQUIRED (0x2) | ES_SYSTEM_REQUIRED (0x1).
		// The state is per-thread, so the PowerShell thread that sets it
		// must stay alive; the loop below does that.
		script := `
Add-Type -Namespace Win32 -Name Power -MemberDefinition '[DllImport("kernel32.dll")] public static extern uint SetThreadExecutionState(uint f);'
[void][Win32.Power]::SetThreadExecutionState([uint32]2147483651)
while (Get-Process -Id ` + pid + ` -ErrorAction SilentlyContinue) { Start-Sleep -Seconds 5 }
`
		return []*exec.Cmd{exec.Command("powershell", "-NoProfile", "-NonInteractive",
			"-WindowStyle", "Hidden", "-Command", script)}, nil
	}

	return nil, fmt.Errorf("sleepctl: unsupported OS %q", runtime.GOOS)
}