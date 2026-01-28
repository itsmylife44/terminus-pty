package session

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/itsmylife44/terminus-pty/internal/pty"
)

type Session struct {
	ID              string
	PTY             *pty.PTY
	Cols            uint16
	Rows            uint16
	CreatedAt       time.Time
	DisconnectedAt  *time.Time
	TmuxSessionName string // tmux session name when TmuxEnabled, empty otherwise
	LastActivityAt  time.Time

	clients           map[*websocket.Conn]string // maps connection to client ID
	clientsMu         sync.RWMutex
	connectedClientId string // current active client ID (empty if no clients)
	broadcast         chan []byte
	done              chan struct{}
	closeOnce         sync.Once
}

func NewSession(id string, p *pty.PTY, cols, rows uint16) *Session {
	now := time.Now()
	s := &Session{
		ID:             id,
		PTY:            p,
		Cols:           cols,
		Rows:           rows,
		CreatedAt:      now,
		LastActivityAt: now,
		clients:        make(map[*websocket.Conn]string),
		broadcast:      make(chan []byte, 256),
		done:           make(chan struct{}),
	}

	go s.readPTY()
	go s.broadcastLoop()

	return s
}

func (s *Session) readPTY() {
	buf := make([]byte, 4096)
	for {
		select {
		case <-s.done:
			return
		default:
			n, err := s.PTY.Read(buf)
			if err != nil {
				s.Close()
				return
			}
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				select {
				case s.broadcast <- data:
				case <-s.done:
					return
				default:
				}
			}
		}
	}
}

func (s *Session) broadcastLoop() {
	for {
		select {
		case <-s.done:
			return
		case data := <-s.broadcast:
			s.broadcastToClients(data)
		}
	}
}

func (s *Session) broadcastToClients(data []byte) {
	s.clientsMu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.clientsMu.RUnlock()

	var failed []*websocket.Conn
	for _, client := range clients {
		if err := client.WriteMessage(websocket.BinaryMessage, data); err != nil {
			failed = append(failed, client)
		}
	}

	for _, client := range failed {
		client.Close()
	}
}

// AddClient registers a new WebSocket client with a client ID.
// Returns the generated client ID.
func (s *Session) AddClient(conn *websocket.Conn, clientID string) {
	s.clientsMu.Lock()
	s.clients[conn] = clientID
	s.connectedClientId = clientID
	s.DisconnectedAt = nil
	s.LastActivityAt = time.Now()
	s.clientsMu.Unlock()
}

// UpdateActivity updates the last activity timestamp.
func (s *Session) UpdateActivity() {
	s.clientsMu.Lock()
	s.LastActivityAt = time.Now()
	s.clientsMu.Unlock()
}

// GetLastActivity returns the last activity timestamp.
func (s *Session) GetLastActivity() time.Time {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.LastActivityAt
}

func (s *Session) RemoveClient(conn *websocket.Conn) {
	s.clientsMu.Lock()
	clientID := s.clients[conn]
	delete(s.clients, conn)
	// Clear connectedClientId if the removed client was the active one
	if s.connectedClientId == clientID {
		s.connectedClientId = ""
	}
	if len(s.clients) == 0 {
		now := time.Now()
		s.DisconnectedAt = &now
	}
	s.clientsMu.Unlock()
}

func (s *Session) ClientCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}

// IsOccupied returns true if there's at least one connected client.
func (s *Session) IsOccupied() bool {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.connectedClientId != ""
}

// ConnectedClientID returns the current active client ID.
func (s *Session) ConnectedClientID() string {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return s.connectedClientId
}

// CloseCode4001 is the WebSocket close code for session takeover.
const CloseCode4001 = 4001

// DisconnectAllClients disconnects all connected clients with a close frame.
// Used for session takeover. Returns the number of clients disconnected.
func (s *Session) DisconnectAllClients(closeCode int, closeMessage string) int {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()

	count := len(s.clients)
	for conn := range s.clients {
		// Send close frame with custom code and message
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCode, closeMessage))
		conn.Close()
	}
	s.clients = make(map[*websocket.Conn]string)
	s.connectedClientId = ""
	return count
}

func (s *Session) Write(data []byte) error {
	_, err := s.PTY.Write(data)
	return err
}

func (s *Session) Resize(cols, rows uint16) error {
	s.Cols = cols
	s.Rows = rows
	return s.PTY.Resize(cols, rows)
}

// Close closes the session. For tmux sessions, it only closes the PTY attachment,
// NOT the underlying tmux session (preserving it for reconnection).
// To fully close including the tmux session, use CloseWithTmux.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)

		s.clientsMu.Lock()
		for client := range s.clients {
			client.Close()
		}
		s.clients = make(map[*websocket.Conn]string)
		s.connectedClientId = ""
		s.clientsMu.Unlock()

		if s.PTY != nil {
			s.PTY.Close()
		}
	})
}

// CloseWithTmux closes the session and kills the tmux session if present.
// Use this for explicit DELETE requests or timeout cleanup.
func (s *Session) CloseWithTmux() {
	s.closeOnce.Do(func() {
		close(s.done)

		s.clientsMu.Lock()
		for client := range s.clients {
			client.Close()
		}
		s.clients = make(map[*websocket.Conn]string)
		s.connectedClientId = ""
		s.clientsMu.Unlock()

		if s.PTY != nil {
			s.PTY.CloseWithTmux()
		}
	})
}

// ReplacePTY replaces the current PTY with a new one (used for tmux reattachment).
func (s *Session) ReplacePTY(newPTY *pty.PTY) {
	// Close old PTY (but not tmux session)
	if s.PTY != nil {
		s.PTY.Close()
	}
	s.PTY = newPTY

	// Restart the read loop with new PTY
	// Note: The old readPTY goroutine will exit on the next Read error
	// We need a fresh done channel for the new PTY
	s.done = make(chan struct{})
	s.closeOnce = sync.Once{}

	go s.readPTY()
	go s.broadcastLoop()
}

func (s *Session) IsClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
