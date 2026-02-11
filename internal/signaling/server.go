package signaling

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Server is the host-side WebSocket server used for signaling.
type Server struct {
	pin      string
	listener net.Listener
	connCh   chan *websocket.Conn
}

// NewServer creates a new signaling server with the given PIN for authentication.
func NewServer(pin string) *Server {
	return &Server{
		pin:    pin,
		connCh: make(chan *websocket.Conn, 1),
	}
}

// Start begins listening on a random port. Returns the assigned port number.
func (s *Server) Start() (int, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, fmt.Errorf("failed to start WS server: %w", err)
	}
	s.listener = listener
	port := listener.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)

	go func() {
		_ = http.Serve(listener, mux)
	}()

	return port, nil
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	pin := r.URL.Query().Get("pin")
	if pin != s.pin {
		http.Error(w, "Invalid PIN", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	// Only accept the first client.
	select {
	case s.connCh <- conn:
	default:
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "already connected"))
		conn.Close()
	}
}

// WaitForClient blocks until a client connects or context is cancelled.
// Returns the raw WebSocket connection for the caller to use directly.
func (s *Server) WaitForClient(ctx context.Context) (*websocket.Conn, error) {
	select {
	case conn := <-s.connCh:
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close shuts down the listener, preventing new connections.
func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
}
