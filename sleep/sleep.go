// Package sleepctl provides a single cross-platform switch for preventing
// the computer from going to sleep.
//
// Implementation is stdlib-only and works by holding a helper process that
// owns the OS-level inhibit request:
//
//	macOS:   caffeinate -i -w <pid>
//	Linux:   systemd-inhibit (requires systemd/logind)
//	Windows: powershell + SetThreadExecutionState
//
// Each helper exits on its own if this process dies, so the inhibit does not
// outlive your program.
package sleep

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
)

var (
	mu     sync.Mutex
	helper *exec.Cmd
)

// SetCanSleep controls whether the system may sleep.
//
//	SetCanSleep(false) - keep the computer awake
//	SetCanSleep(true)  - restore normal sleep behavior
//
// Calls are idempotent and safe for concurrent use. Display sleep / screen
// lock is not prevented, only system sleep and idle sleep.
func SetCanSleep(canSleep bool) error {
	mu.Lock()
	defer mu.Unlock()

	if canSleep {
		release()
		return nil
	}

	// Already inhibiting and helper still running.
	if helper != nil && helper.ProcessState == nil {
		return nil
	}

	cmd, err := buildCommand()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sleepctl: failed to start inhibitor: %w", err)
	}
	helper = cmd

	// Reap the process when it exits so ProcessState gets populated.
	go func(c *exec.Cmd) { _ = c.Wait() }(cmd)
	return nil
}

func release() {
	if helper == nil {
		return
	}
	if helper.Process != nil {
		_ = helper.Process.Kill()
	}
	helper = nil
}

func buildCommand() (*exec.Cmd, error) {
	pid := strconv.Itoa(os.Getpid())

	switch runtime.GOOS {
	case "darwin":
		// -i: prevent idle sleep, -w: exit when pid exits.
		return exec.Command("caffeinate", "-i", "-w", pid), nil

	case "linux":
		if _, err := exec.LookPath("systemd-inhibit"); err != nil {
			return nil, fmt.Errorf("sleepctl: systemd-inhibit not found: %w", err)
		}
		// Hold the inhibitor lock while the parent is alive.
		loop := "while kill -0 " + pid + " 2>/dev/null; do sleep 5; done"
		return exec.Command("systemd-inhibit",
			"--what=sleep:idle",
			"--who=sleepctl",
			"--why=Application requested no sleep",
			"--mode=block",
			"sh", "-c", loop), nil

	case "windows":
		// ES_CONTINUOUS (0x80000000) | ES_SYSTEM_REQUIRED (0x1).
		// The execution state is per-thread, so the same PowerShell thread
		// that sets it must stay alive; the loop below does that.
		script := `
Add-Type -Namespace Win32 -Name Power -MemberDefinition '[DllImport("kernel32.dll")] public static extern uint SetThreadExecutionState(uint f);'
[void][Win32.Power]::SetThreadExecutionState([uint32]2147483649)
while (Get-Process -Id ` + pid + ` -ErrorAction SilentlyContinue) { Start-Sleep -Seconds 5 }
`
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive",
			"-WindowStyle", "Hidden", "-Command", script)
		return cmd, nil
	}

	return nil, fmt.Errorf("sleepctl: unsupported OS %q", runtime.GOOS)
}