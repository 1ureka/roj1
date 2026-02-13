// Package signaling orchestrates the complete signaling phase — from user input
// to an established P2P tunnel. All WebSocket and SDP/ICE details are internal;
// callers receive a ready-to-use Transport.
package signaling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v4"

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

	// 組裝 sender / receiver
	s := &sender{tr: tr, conn: wsConn}
	r := &receiver{tr: tr, conn: wsConn, sender: s}

	// 註冊 ICE candidate 回調 → 透過 sender 發送
	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			data, _ := json.Marshal(c.ToJSON())
			// 錯誤在此被忽略（見 §5.1），不是阿，report5 之後要刪除ㄝ，寫下來好嗎?
			s.sendCandidate(string(data))
		}
	})

	// 啟動 receiver 迴圈（背景 goroutine）
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.watch() // 該 routine 會在 defer wsConn.Close() 後因為 ReadJSON 失敗而釋放，不須 ctx
	}()

	// Host 先發送 Offer
	if err := s.sendOffer(); err != nil {
		tr.Close()
		return nil, fmt.Errorf("發送 Offer 失敗: %w", err)
	}

	// 等待結果
	select {
	case <-tr.Ready():
		util.Logf("WebRTC DataChannel 已建立， WS 即將關閉")
		return tr, nil

	case err := <-errCh:
		tr.Close()
		return nil, fmt.Errorf("signaling 失敗: %w", err)

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

	// 組裝 sender / receiver
	s := &sender{tr: tr, conn: wsConn}
	r := &receiver{tr: tr, conn: wsConn, sender: s}

	// 註冊 ICE candidate 回調 → 透過 sender 發送
	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			data, _ := json.Marshal(c.ToJSON())
			// 錯誤在此被忽略（見 §5.1），不是阿，report5 之後要刪除ㄝ，寫下來好嗎?
			s.sendCandidate(string(data))
		}
	})

	// 啟動 receiver 迴圈（背景 goroutine）
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.watch()
	}()

	// 等待結果
	select {
	case <-tr.Ready():
		util.Logf("WebRTC DataChannel 已建立， WS 即將關閉")
		return tr, nil

	case err := <-errCh:
		tr.Close()
		return nil, fmt.Errorf("signaling 失敗: %w", err)

	case <-ctx.Done():
		tr.Close()
		return nil, ctx.Err()
	}
}
