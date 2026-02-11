package signaling

import (
	"context"
	"fmt"

	"github.com/gorilla/websocket"
)

// Connect dials the given WebSocket URL and returns the connection.
// The URL should include the PIN as a query parameter, e.g.:
//
//	wss://example.devtunnels.ms/ws?pin=1234
func Connect(ctx context.Context, url string) (*websocket.Conn, error) {
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to WS server: %w", err)
	}
	return conn, nil
}
