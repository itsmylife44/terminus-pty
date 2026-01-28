package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

// ErrTmuxNotInstalled is returned when tmux is not available on the system.
var ErrTmuxNotInstalled = fmt.Errorf("tmux is not installed or not in PATH")

// CheckInstalled verifies tmux is available in PATH.
func CheckInstalled() error {
	_, err := exec.LookPath("tmux")
	if err != nil {
		return ErrTmuxNotInstalled
	}
	return nil
}

// SessionExists checks if a tmux session with the given name exists.
func SessionExists(sessionName string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	return cmd.Run() == nil
}

// SpawnSession creates a new tmux session with the given name and command,
// returning a PTY file descriptor attached to it.
// The session runs detached, and we attach to it via a control mode connection.
func SpawnSession(sessionName, command string, args []string, cols, rows uint16, workdir string) (*os.File, *exec.Cmd, error) {
	// Build the full command to run inside tmux
	fullCmd := command
	if len(args) > 0 {
		fullCmd = command + " " + strings.Join(args, " ")
	}

	// Create tmux session detached
	createArgs := []string{
		"new-session",
		"-d", // detached
		"-s", sessionName,
		"-x", fmt.Sprintf("%d", cols),
		"-y", fmt.Sprintf("%d", rows),
	}
	if workdir != "" {
		createArgs = append(createArgs, "-c", workdir)
	}
	createArgs = append(createArgs, fullCmd)

	createCmd := exec.Command("tmux", createArgs...)
	createCmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)
	if err := createCmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("failed to create tmux session: %w", err)
	}

	// Attach to the session with a PTY
	return AttachSession(sessionName, cols, rows)
}

// AttachSession attaches to an existing tmux session, returning a PTY.
func AttachSession(sessionName string, cols, rows uint16) (*os.File, *exec.Cmd, error) {
	if !SessionExists(sessionName) {
		return nil, nil, fmt.Errorf("tmux session %q does not exist", sessionName)
	}

	// Attach to the tmux session
	attachCmd := exec.Command("tmux", "attach-session", "-t", sessionName)
	attachCmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.StartWithSize(attachCmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to attach to tmux session: %w", err)
	}

	return ptmx, attachCmd, nil
}

// KillSession terminates a tmux session.
func KillSession(sessionName string) error {
	if !SessionExists(sessionName) {
		return nil // Session already gone, that's fine
	}
	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	return cmd.Run()
}

// ResizeSession resizes the tmux session window.
func ResizeSession(sessionName string, cols, rows uint16) error {
	// Resize the tmux window
	cmd := exec.Command("tmux", "resize-window", "-t", sessionName, "-x", fmt.Sprintf("%d", cols), "-y", fmt.Sprintf("%d", rows))
	return cmd.Run()
}
