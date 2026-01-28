package session

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/itsmylife44/terminus-pty/internal/pty"
)

type Session struct {
	ID             string
	PTY            *pty.PTY
	Cols           uint16
	Rows           uint16
	CreatedAt      time.Time
	DisconnectedAt *time.Time

	clients   map[*websocket.Conn]bool
	clientsMu sync.RWMutex
	broadcast chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

func NewSession(id string, p *pty.PTY, cols, rows uint16) *Session {
	s := &Session{
		ID:        id,
		PTY:       p,
		Cols:      cols,
		Rows:      rows,
		CreatedAt: time.Now(),
		clients:   make(map[*websocket.Conn]bool),
		broadcast: make(chan []byte, 256),
		done:      make(chan struct{}),
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

func (s *Session) AddClient(conn *websocket.Conn) {
	s.clientsMu.Lock()
	s.clients[conn] = true
	s.DisconnectedAt = nil
	s.clientsMu.Unlock()
}

func (s *Session) RemoveClient(conn *websocket.Conn) {
	s.clientsMu.Lock()
	delete(s.clients, conn)
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

func (s *Session) Write(data []byte) error {
	_, err := s.PTY.Write(data)
	return err
}

func (s *Session) Resize(cols, rows uint16) error {
	s.Cols = cols
	s.Rows = rows
	return s.PTY.Resize(cols, rows)
}

func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.done)

		s.clientsMu.Lock()
		for client := range s.clients {
			client.Close()
		}
		s.clients = make(map[*websocket.Conn]bool)
		s.clientsMu.Unlock()

		if s.PTY != nil {
			s.PTY.Close()
		}
	})
}

func (s *Session) IsClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
