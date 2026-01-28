package session

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/itsmylife44/terminus-pty/internal/pty"
	"github.com/itsmylife44/terminus-pty/internal/tmux"
	"github.com/rs/xid"
)

type PoolConfig struct {
	SessionTimeout      time.Duration
	CleanupInterval     time.Duration
	DefaultCommand      string
	DefaultArgs         []string
	DefaultWorkdir      string
	TmuxEnabled         bool
	MaxInactive         time.Duration // Max inactivity time for tmux session cleanup
	TmuxCleanupInterval time.Duration // Interval for tmux cleanup goroutine
}

type Pool struct {
	config   PoolConfig
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewPool(config PoolConfig) *Pool {
	return &Pool{
		config:   config,
		sessions: make(map[string]*Session),
	}
}

func (p *Pool) Create(cols, rows uint16, command string, args []string, workdir string) (*Session, error) {
	cmd := command
	if cmd == "" {
		cmd = p.config.DefaultCommand
	}

	cmdArgs := args
	if len(cmdArgs) == 0 {
		cmdArgs = p.config.DefaultArgs
	}
	// If still no args and command looks like a shell, use shell defaults
	if len(cmdArgs) == 0 && (strings.HasSuffix(cmd, "sh") || strings.Contains(cmd, "/sh")) {
		cmdArgs = []string{"-l", "-i"}
	}

	wd := workdir
	if wd == "" {
		wd = p.config.DefaultWorkdir
	}

	id := "pty_" + xid.New().String()
	var ptty *pty.PTY
	var tmuxSessionName string
	var err error

	if p.config.TmuxEnabled {
		// Spawn PTY inside tmux for persistence
		tmuxSessionName = id // Use session ID as tmux session name
		ptty, err = pty.SpawnWithTmux(tmuxSessionName, cmd, cmdArgs, cols, rows, wd)
		if err != nil {
			return nil, fmt.Errorf("tmux spawn failed: %w", err)
		}
		slog.Info("Session created with tmux", "id", id, "tmux_session", tmuxSessionName, "command", cmd, "args", cmdArgs, "workdir", wd, "cols", cols, "rows", rows)
	} else {
		// Direct PTY spawn (existing behavior)
		ptty, err = pty.Spawn(cmd, cmdArgs, cols, rows, wd)
		if err != nil {
			return nil, err
		}
		slog.Info("Session created", "id", id, "command", cmd, "args", cmdArgs, "workdir", wd, "cols", cols, "rows", rows)
	}

	session := NewSession(id, ptty, cols, rows)
	session.TmuxSessionName = tmuxSessionName

	p.mu.Lock()
	p.sessions[id] = session
	p.mu.Unlock()

	return session, nil
}

// ReattachTmux reattaches to an existing tmux session. Only works if TmuxEnabled.
func (p *Pool) ReattachTmux(session *Session, cols, rows uint16) error {
	if !p.config.TmuxEnabled || session.TmuxSessionName == "" {
		return fmt.Errorf("session %s is not a tmux session", session.ID)
	}

	// Check if tmux session still exists
	if !tmux.SessionExists(session.TmuxSessionName) {
		return fmt.Errorf("tmux session %s no longer exists", session.TmuxSessionName)
	}

	// If PTY is already closed, reattach
	if session.IsClosed() {
		return fmt.Errorf("session is closed and cannot be reattached")
	}

	// Create new PTY attachment to existing tmux session
	ptty, err := pty.AttachTmux(session.TmuxSessionName, cols, rows)
	if err != nil {
		return fmt.Errorf("failed to reattach to tmux session: %w", err)
	}

	// Replace the PTY in the session
	session.ReplacePTY(ptty)

	slog.Info("Reattached to tmux session", "id", session.ID, "tmux_session", session.TmuxSessionName)
	return nil
}

func (p *Pool) Get(id string) (*Session, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	session, ok := p.sessions[id]
	if ok && session.IsClosed() {
		return nil, false
	}
	return session, ok
}

func (p *Pool) Remove(id string) {
	p.mu.Lock()
	if session, ok := p.sessions[id]; ok {
		// Explicit DELETE should kill tmux session too
		session.CloseWithTmux()
		delete(p.sessions, id)
	}
	p.mu.Unlock()
}

func (p *Pool) StartCleanup(ctx context.Context) {
	ticker := time.NewTicker(p.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.cleanup()
		}
	}
}

func (p *Pool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var toRemove []string

	for id, session := range p.sessions {
		if session.IsClosed() {
			toRemove = append(toRemove, id)
			continue
		}

		if session.DisconnectedAt != nil && session.ClientCount() == 0 {
			if now.Sub(*session.DisconnectedAt) > p.config.SessionTimeout {
				toRemove = append(toRemove, id)
				slog.Info("Session expired", "id", id, "disconnected_for", now.Sub(*session.DisconnectedAt), "tmux", session.TmuxSessionName != "")
			}
		}
	}

	for _, id := range toRemove {
		if session, ok := p.sessions[id]; ok {
			// Use CloseWithTmux to kill tmux sessions on timeout
			session.CloseWithTmux()
			delete(p.sessions, id)
		}
	}
}

func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, session := range p.sessions {
		// On server shutdown, kill tmux sessions too
		session.CloseWithTmux()
		delete(p.sessions, id)
	}

	slog.Info("All sessions closed")
}

func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessions)
}

// StartTmuxCleanup starts the background goroutine that cleans up orphaned tmux sessions.
// This cleans tmux sessions with "pty_" prefix that have no clients and exceed max-inactive.
func (p *Pool) StartTmuxCleanup(ctx context.Context) {
	if !p.config.TmuxEnabled {
		return // No cleanup needed if tmux is disabled
	}

	interval := p.config.TmuxCleanupInterval
	if interval < 10*time.Minute {
		interval = 10 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("Starting tmux cleanup goroutine", "interval", interval, "max_inactive", p.config.MaxInactive)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Tmux cleanup goroutine stopped")
			return
		case <-ticker.C:
			p.cleanupTmuxSessions()
		}
	}
}

// cleanupTmuxSessions checks for orphaned tmux sessions and kills them.
func (p *Pool) cleanupTmuxSessions() {
	// List all tmux sessions with our prefix
	sessions, err := tmux.ListSessions("pty_")
	if err != nil {
		slog.Error("Failed to list tmux sessions", "error", err)
		return
	}

	if len(sessions) == 0 {
		return
	}

	now := time.Now()
	var killed []string

	p.mu.RLock()
	for _, tmuxSessionName := range sessions {
		// Check if this tmux session is tracked in our pool
		var trackedSession *Session
		for _, s := range p.sessions {
			if s.TmuxSessionName == tmuxSessionName {
				trackedSession = s
				break
			}
		}

		// If session is in pool, check activity
		if trackedSession != nil {
			// Session is tracked - check if it's inactive
			if trackedSession.ClientCount() == 0 {
				lastActivity := trackedSession.GetLastActivity()
				if now.Sub(lastActivity) > p.config.MaxInactive {
					killed = append(killed, tmuxSessionName)
				}
			}
		} else {
			// Session is not in our pool but has our prefix - orphaned
			// Check if it has no attached clients
			clientCount := tmux.GetSessionClientCount(tmuxSessionName)
			if clientCount == 0 {
				killed = append(killed, tmuxSessionName)
			}
		}
	}
	p.mu.RUnlock()

	// Kill orphaned/inactive sessions outside the lock
	for _, sessionName := range killed {
		if err := tmux.KillSession(sessionName); err != nil {
			slog.Error("Failed to kill tmux session", "session", sessionName, "error", err)
		} else {
			slog.Info("Killed inactive tmux session", "session", sessionName)
		}

		// Also remove from pool if tracked
		p.mu.Lock()
		for id, s := range p.sessions {
			if s.TmuxSessionName == sessionName {
				s.Close()
				delete(p.sessions, id)
				break
			}
		}
		p.mu.Unlock()
	}

	if len(killed) > 0 {
		slog.Info("Tmux cleanup completed", "killed", len(killed))
	}
}
