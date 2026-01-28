package pty

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/itsmylife44/terminus-pty/internal/tmux"
)

type PTY struct {
	File            *os.File
	Cmd             *exec.Cmd
	TmuxSessionName string // Non-empty when using tmux mode
}

type Size struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Spawn creates a direct PTY without tmux.
func Spawn(command string, args []string, cols, rows uint16, workdir string) (*PTY, error) {
	// Validate command exists
	if _, err := exec.LookPath(command); err != nil {
		return nil, fmt.Errorf("command not found: %s", command)
	}

	cmd := exec.Command(command, args...)

	if workdir != "" {
		cmd.Dir = workdir
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			cmd.Dir = home
		}
	}

	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, err
	}

	return &PTY{
		File: ptmx,
		Cmd:  cmd,
	}, nil
}

// SpawnWithTmux creates a PTY inside a tmux session for persistence.
func SpawnWithTmux(sessionName, command string, args []string, cols, rows uint16, workdir string) (*PTY, error) {
	file, cmd, err := tmux.SpawnSession(sessionName, command, args, cols, rows, workdir)
	if err != nil {
		return nil, err
	}

	return &PTY{
		File:            file,
		Cmd:             cmd,
		TmuxSessionName: sessionName,
	}, nil
}

// AttachTmux reattaches to an existing tmux session.
func AttachTmux(sessionName string, cols, rows uint16) (*PTY, error) {
	file, cmd, err := tmux.AttachSession(sessionName, cols, rows)
	if err != nil {
		return nil, err
	}

	return &PTY{
		File:            file,
		Cmd:             cmd,
		TmuxSessionName: sessionName,
	}, nil
}

func (p *PTY) Resize(cols, rows uint16) error {
	// If tmux mode, also resize the tmux session
	if p.TmuxSessionName != "" {
		if err := tmux.ResizeSession(p.TmuxSessionName, cols, rows); err != nil {
			// Log but don't fail - the PTY resize is more important
			_ = err
		}
	}
	return pty.Setsize(p.File, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
}

// Close closes the PTY connection but does NOT kill the tmux session.
// To kill the tmux session, use CloseWithTmux.
func (p *PTY) Close() error {
	// Kill the attach process (tmux attach or shell)
	if p.Cmd != nil && p.Cmd.Process != nil {
		_ = p.Cmd.Process.Kill()
		_, _ = p.Cmd.Process.Wait()
	}
	if p.File != nil {
		return p.File.Close()
	}
	return nil
}

// CloseWithTmux closes the PTY and kills the tmux session if present.
func (p *PTY) CloseWithTmux() error {
	// First close the PTY
	err := p.Close()

	// Then kill the tmux session if this is a tmux-backed PTY
	if p.TmuxSessionName != "" {
		if killErr := tmux.KillSession(p.TmuxSessionName); killErr != nil {
			if err == nil {
				err = killErr
			}
		}
	}
	return err
}

// IsTmux returns true if this PTY is backed by a tmux session.
func (p *PTY) IsTmux() bool {
	return p.TmuxSessionName != ""
}

func (p *PTY) Read(buf []byte) (int, error) {
	return p.File.Read(buf)
}

func (p *PTY) Write(data []byte) (int, error) {
	return p.File.Write(data)
}
