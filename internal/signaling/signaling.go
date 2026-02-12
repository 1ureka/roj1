// Package signaling orchestrates the complete signaling phase — from user input
// to an established P2P tunnel. All WebSocket and SDP/ICE details are internal;
// callers receive a ready-to-use Transport.
package signaling

import (
	"context"
	"fmt"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// EstablishAsHost executes the full host-side signaling flow:
//  1. Generate a random PIN
//  2. Start a WS server on a random port
//  3. Print Port / PIN info
//  4. Wait for the client to connect
//  5. Create a Transport
//  6. Perform SDP/ICE exchange
//  7. Wait for the DataChannel to be ready
//  8. Close the WS server and connection (resource cleanup)
//  9. Return the ready Transport
func EstablishAsHost(ctx context.Context) (*transport.Transport, error) {
	// 1. Generate PIN & start WS server.
	pin := generatePIN(4)
	srv := newServer(pin)
	wsPort, err := srv.start()
	if err != nil {
		return nil, err
	}
	defer srv.close()

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║        WebSocket Signaling Server        ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║  Port : %-32d ║\n", wsPort)
	fmt.Printf("║  PIN  : %-32s ║\n", pin)
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Println("║  提示：使用 VS Code Port Forwarding      ║")
	fmt.Println("║  將此 port 轉發到公網                    ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("等待 Client 連線...")

	// 2. Wait for client WS connection.
	wsConn, err := srv.waitForClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("等待 Client 失敗: %w", err)
	}
	defer wsConn.Close()
	util.Logf("Client 已連線")

	// 3. Create Transport.
	tr, err := transport.NewTransport(ctx)
	if err != nil {
		return nil, fmt.Errorf("建立 Transport 失敗: %w", err)
	}

	// 4. SDP/ICE exchange.
	if err := hostExchange(wsConn, tr); err != nil {
		tr.Close()
		return nil, fmt.Errorf("signaling 失敗: %w", err)
	}

	// 5. Release listener resources early.
	srv.close()
	util.Logf("WebRTC DataChannel 已建立，WS 已關閉")

	return tr, nil
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
	fmt.Println("正在連線到 Host...")
	wsConn, err := connect(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	defer wsConn.Close()
	util.Logf("WS 已連線: %s", wsURL)

	// 2. Create Transport.
	tr, err := transport.NewTransport(ctx)
	if err != nil {
		return nil, fmt.Errorf("建立 Transport 失敗: %w", err)
	}

	// 3. SDP/ICE exchange.
	if err := clientExchange(wsConn, tr); err != nil {
		tr.Close()
		return nil, fmt.Errorf("signaling 失敗: %w", err)
	}

	util.Logf("WebRTC DataChannel 已建立，WS 已關閉")
	return tr, nil
}
