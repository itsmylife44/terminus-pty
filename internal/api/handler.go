package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/itsmylife44/terminus-pty/internal/auth"
	"github.com/itsmylife44/terminus-pty/internal/session"
)

// generateClientID creates a random 16-character hex string for client identification.
func generateClientID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type Handler struct {
	pool *session.Pool
	auth *auth.BasicAuth
}

func NewHandler(pool *session.Pool, authenticator *auth.BasicAuth) http.Handler {
	h := &Handler{
		pool: pool,
		auth: authenticator,
	}

	r := mux.NewRouter()

	r.HandleFunc("/health", h.health).Methods("GET")
	r.HandleFunc("/pty", h.createSession).Methods("POST")
	r.HandleFunc("/pty/{id}", h.getSession).Methods("GET")
	r.HandleFunc("/pty/{id}", h.updateSession).Methods("PUT")
	r.HandleFunc("/pty/{id}", h.deleteSession).Methods("DELETE")
	r.HandleFunc("/pty/{id}/connect", h.connectSession).Methods("GET")
	r.HandleFunc("/pty/{id}/takeover", h.takeoverSession).Methods("POST")

	if authenticator != nil {
		return authenticator.Middleware(r)
	}
	return r
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"sessions": h.pool.Count(),
	})
}

type CreateRequest struct {
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Workdir string   `json:"workdir,omitempty"`
}

type CreateResponse struct {
	ID string `json:"id"`
}

func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Cols == 0 {
		req.Cols = 80
	}
	if req.Rows == 0 {
		req.Rows = 24
	}

	sess, err := h.pool.Create(req.Cols, req.Rows, req.Command, req.Args, req.Workdir)
	if err != nil {
		slog.Error("Failed to create session", "error", err)
		http.Error(w, "Failed to create session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CreateResponse{ID: sess.ID})
}

type UpdateRequest struct {
	Size *struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	} `json:"size,omitempty"`
}

func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	sess, ok := h.pool.Get(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Size != nil {
		if err := sess.Resize(req.Size.Cols, req.Size.Rows); err != nil {
			slog.Error("Failed to resize", "id", id, "error", err)
			http.Error(w, "Failed to resize", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	h.pool.Remove(id)
	w.WriteHeader(http.StatusOK)
}

// SessionInfoResponse is the response for GET /pty/{id}
type SessionInfoResponse struct {
	ID         string `json:"id"`
	Occupied   bool   `json:"occupied"`
	ClientInfo string `json:"clientInfo,omitempty"`
	Cols       uint16 `json:"cols"`
	Rows       uint16 `json:"rows"`
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	sess, ok := h.pool.Get(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SessionInfoResponse{
		ID:         sess.ID,
		Occupied:   sess.IsOccupied(),
		ClientInfo: sess.ConnectedClientID(),
		Cols:       sess.Cols,
		Rows:       sess.Rows,
	})
}

// TakeoverRequest is the request body for POST /pty/{id}/takeover
type TakeoverRequest struct {
	ClientID string `json:"clientId,omitempty"`
}

// TakeoverResponse is the response for POST /pty/{id}/takeover
type TakeoverResponse struct {
	Success           bool   `json:"success"`
	DisconnectedCount int    `json:"disconnectedCount"`
	NewClientID       string `json:"newClientId"`
}

func (h *Handler) takeoverSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	sess, ok := h.pool.Get(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var req TakeoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body - will auto-generate client ID
		req = TakeoverRequest{}
	}

	// Generate client ID if not provided
	newClientID := req.ClientID
	if newClientID == "" {
		newClientID = generateClientID()
	}

	// Disconnect all current clients with takeover close code
	disconnected := sess.DisconnectAllClients(session.CloseCode4001, "session taken over")

	slog.Info("Session takeover", "id", id, "disconnected", disconnected, "newClientId", newClientID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TakeoverResponse{
		Success:           true,
		DisconnectedCount: disconnected,
		NewClientID:       newClientID,
	})
}

func (h *Handler) connectSession(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	sess, ok := h.pool.Get(id)
	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WebSocket upgrade failed", "error", err)
		return
	}

	// Generate a unique client ID for this connection
	clientID := generateClientID()

	slog.Info("Client connected", "id", id, "remote", r.RemoteAddr, "clientId", clientID)
	sess.AddClient(conn, clientID)

	defer func() {
		sess.RemoveClient(conn)
		conn.Close()
		slog.Info("Client disconnected", "id", id, "remote", r.RemoteAddr, "clientId", clientID)
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := sess.Write(data); err != nil {
			return
		}
	}
}
