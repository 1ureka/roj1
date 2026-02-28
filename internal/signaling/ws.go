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

// server is the host-side WebSocket server used during signaling (private).
type server struct {
	listener net.Listener
	connCh   chan *websocket.Conn
}

// start begins listening on the given address (e.g. ":0", "127.0.0.1:9000").
// Returns the assigned port number.
func (s *server) start(addr string) (int, error) {
	listener, err := net.Listen("tcp", addr)
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
