package pty

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type PTY struct {
	File *os.File
	Cmd  *exec.Cmd
}

type Size struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

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

func (p *PTY) Resize(cols, rows uint16) error {
	return pty.Setsize(p.File, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
}

func (p *PTY) Close() error {
	if p.Cmd != nil && p.Cmd.Process != nil {
		_ = p.Cmd.Process.Kill()
		_, _ = p.Cmd.Process.Wait()
	}
	if p.File != nil {
		return p.File.Close()
	}
	return nil
}

func (p *PTY) Read(buf []byte) (int, error) {
	return p.File.Read(buf)
}

func (p *PTY) Write(data []byte) (int, error) {
	return p.File.Write(data)
}
