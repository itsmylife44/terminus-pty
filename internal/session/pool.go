package session

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/itsmylife44/terminus-pty/internal/pty"
	"github.com/rs/xid"
)

type PoolConfig struct {
	SessionTimeout  time.Duration
	CleanupInterval time.Duration
	DefaultShell    string
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

func (p *Pool) Create(cols, rows uint16, command string) (*Session, error) {
	shell := command
	if shell == "" {
		shell = p.config.DefaultShell
	}

	ptty, err := pty.Spawn(shell, cols, rows, "")
	if err != nil {
		return nil, err
	}

	id := "pty_" + xid.New().String()
	session := NewSession(id, ptty, cols, rows)

	p.mu.Lock()
	p.sessions[id] = session
	p.mu.Unlock()

	slog.Info("Session created", "id", id, "shell", shell, "cols", cols, "rows", rows)
	return session, nil
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
		session.Close()
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
				slog.Info("Session expired", "id", id, "disconnected_for", now.Sub(*session.DisconnectedAt))
			}
		}
	}

	for _, id := range toRemove {
		if session, ok := p.sessions[id]; ok {
			session.Close()
			delete(p.sessions, id)
		}
	}
}

func (p *Pool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id, session := range p.sessions {
		session.Close()
		delete(p.sessions, id)
	}

	slog.Info("All sessions closed")
}

func (p *Pool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessions)
}
