package signaling

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// server is the host-side WebSocket server used during signaling (private).
type server struct {
	pin      string
	listener net.Listener
	connCh   chan *websocket.Conn
}

// newServer creates a new signaling server with the given PIN for authentication.
func newServer(pin string) *server {
	return &server{
		pin:    pin,
		connCh: make(chan *websocket.Conn, 1),
	}
}

// start begins listening on a random port. Returns the assigned port number.
func (s *server) start() (int, error) {
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

func (s *server) handleWS(w http.ResponseWriter, r *http.Request) {
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

// waitForClient blocks until a client connects or context is cancelled.
func (s *server) waitForClient(ctx context.Context) (*websocket.Conn, error) {
	select {
	case conn := <-s.connCh:
		return conn, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// close shuts down the listener, preventing new connections.
func (s *server) close() {
	if s.listener != nil {
		s.listener.Close()
	}
}

// connect dials the given WebSocket URL and returns the connection (private).
func connect(ctx context.Context, url string) (*websocket.Conn, error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to WS server: %w", err)
	}
	return conn, nil
}

// generatePIN returns a random numeric PIN of the specified length.
func generatePIN(length int) string {
	digits := make([]byte, length)
	for i := range digits {
		n, _ := rand.Int(rand.Reader, big.NewInt(10))
		digits[i] = byte('0') + byte(n.Int64())
	}
	return string(digits)
}
