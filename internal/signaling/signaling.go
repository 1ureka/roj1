// Package signaling orchestrates the complete signaling phase — from user input
// to an established P2P tunnel. All WebSocket and SDP/ICE details are internal;
// callers receive a ready-to-use Transport.
package signaling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// EstablishAsHost executes the full host-side signaling flow:
//  1. Start a WS server on a random port
//  2. Print port info
//  3. Wait for the client to connect
//  4. Create a Transport
//  5. Perform SDP/ICE exchange
//  6. Wait for the DataChannel to be ready
//  7. Close the WS server and connection (resource cleanup)
//  8. Return the ready Transport
func EstablishAsHost(ctx context.Context) (*transport.Transport, error) {
	// 1. Start WS server.
	srv := &server{connCh: make(chan *websocket.Conn, 1)}
	wsPort, err := srv.start()
	if err != nil {
		return nil, err
	}
	defer srv.close()

	// 2. Print port info.
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║        WebSocket Signaling Server        ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║  Port : %-32d ║\n", wsPort)
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Println("║  提示：使用 VS Code Port Forwarding      ║")
	fmt.Println("║  將此 port 轉發到公網                    ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("等待 Client 連線...")

	// 3. Wait for client WS connection.
	wsConn, err := srv.waitForClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for client: %w", err)
	}
	defer wsConn.Close()
	util.Logf("client connected")

	// 4. Create Transport.
	tr, err := transport.NewTransport(ctx)
	if err != nil {
		return nil, fmt.Errorf("建立 Transport 失敗: %w", err)
	}

	// 5. Perform SDP/ICE exchange.
	// Assemble sender and receiver.
	s := &sender{tr: tr, conn: wsConn}
	r := &receiver{tr: tr, conn: wsConn, sender: s}

	// Register ICE candidate callback — forward via sender.
	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			data, _ := json.Marshal(c.ToJSON())
			// Error intentionally ignored: sendCandidate is best-effort.
			s.sendCandidate(string(data))
		}
	})

	// Start receiver loop (background goroutine).
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.watch() // Exits when wsConn is closed (deferred above); no ctx needed.
	}()

	// Host sends the Offer first.
	if err := s.sendOffer(); err != nil {
		tr.Close()
		return nil, fmt.Errorf("failed to send Offer: %w", err)
	}

	// Wait for result.
	select {
	case <-tr.Ready():
		util.Logf("WebRTC DataChannel established, closing WS")
		return tr, nil

	case err := <-errCh:
		tr.Close()
		return nil, fmt.Errorf("signaling failed: %w", err)

	case <-ctx.Done():
		tr.Close()
		return nil, ctx.Err()
	}
}

// EstablishAsClient executes the full client-side signaling flow:
//  1. Connect to the host's WS server
//  2. Create a Transport
//  3. Perform SDP/ICE exchange
//  4. Wait for the DataChannel to be ready
//  5. Close the WS connection (resource cleanup)
//  6. Return the ready Transport
func EstablishAsClient(ctx context.Context, wsURL string) (*transport.Transport, error) {
	// 1. Connect to WS server.
	fmt.Println("Connecting to Host...")
	wsConn, err := connect(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	defer wsConn.Close()
	util.Logf("WS connected: %s", wsURL)

	// 2. Create Transport.
	tr, err := transport.NewTransport(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create Transport: %w", err)
	}

	// 3. Perform SDP/ICE exchange.
	// Assemble sender and receiver.
	s := &sender{tr: tr, conn: wsConn}
	r := &receiver{tr: tr, conn: wsConn, sender: s}

	// Register ICE candidate callback — forward via sender.
	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			data, _ := json.Marshal(c.ToJSON())
			// Error intentionally ignored: sendCandidate is best-effort.
			s.sendCandidate(string(data))
		}
	})

	// Start receiver loop (background goroutine).
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.watch() // Exits when wsConn is closed (deferred above); no ctx needed.
	}()

	// Wait for result.
	select {
	case <-tr.Ready():
		util.Logf("WebRTC DataChannel established, closing WS")
		return tr, nil

	case err := <-errCh:
		tr.Close()
		return nil, fmt.Errorf("signaling failed: %w", err)

	case <-ctx.Done():
		tr.Close()
		return nil, ctx.Err()
	}
}
