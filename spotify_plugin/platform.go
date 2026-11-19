package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func localDeviceName() string {
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "scutil", "--get", "ComputerName").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
			return strings.TrimSpace(string(out))
		}
	}
	name, _ := os.Hostname()
	return name
}

// Start the desktop handler without inheriting protocol stdin/stdout. Reap the
// launcher asynchronously: Spotify itself may remain running indefinitely.
func launchCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not launch %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func openSpotify() error {
	switch runtime.GOOS {
	case "windows":
		return launchCommand("rundll32.exe", "url.dll,FileProtocolHandler", "spotify:")
	case "darwin":
		return launchCommand("open", "-a", "Spotify")
	case "linux":
		return launchCommand("xdg-open", "spotify:")
	default:
		return fmt.Errorf("automatic Spotify launch is unsupported on %s; open Spotify manually", runtime.GOOS)
	}
}

func openBrowser(address string) error {
	switch runtime.GOOS {
	case "windows":
		return launchCommand("rundll32.exe", "url.dll,FileProtocolHandler", address)
	case "darwin":
		return launchCommand("open", address)
	case "linux":
		return launchCommand("xdg-open", address)
	default:
		return fmt.Errorf("open the authorization URL manually")
	}
}
