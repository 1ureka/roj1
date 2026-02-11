// Package app contains the top-level orchestration for host and client roles.
package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/signaling"
	"github.com/1ureka/1ureka.net.p2p/internal/tunnel"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
	webrtcpkg "github.com/1ureka/1ureka.net.p2p/internal/webrtc"
)

// RunHost orchestrates the full host lifecycle:
//  1. Start WS server with a random PIN
//  2. Wait for the client to connect via WebSocket
//  3. Perform WebRTC signaling (SDP + ICE exchange)
//  4. Close WebSocket once DataChannel opens
//  5. Set up backpressure and dispatcher
//  6. Forward traffic until shutdown
func RunHost(ctx context.Context, targetPort int) error {
	// ── 1. Generate PIN & start WS server ──────────────────────────────
	pin := generatePIN(4)
	server := signaling.NewServer(pin)
	wsPort, err := server.Start()
	if err != nil {
		return err
	}
	defer server.Close()

	targetAddr := fmt.Sprintf("127.0.0.1:%d", targetPort)

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

	// ── 2. Wait for client WS connection ───────────────────────────────
	wsConn, err := server.WaitForClient(ctx)
	if err != nil {
		return fmt.Errorf("等待 Client 失敗: %w", err)
	}
	defer wsConn.Close()
	util.Logf("Client 已連線")

	// ── 3. Create PeerConnection & DataChannel ─────────────────────────
	pc, err := webrtcpkg.NewPeerConnection()
	if err != nil {
		return fmt.Errorf("建立 PeerConnection 失敗: %w", err)
	}
	defer pc.Close()

	dc, err := webrtcpkg.CreateDataChannel(pc)
	if err != nil {
		return fmt.Errorf("建立 DataChannel 失敗: %w", err)
	}

	// DC open signal.
	dcOpenCh := make(chan struct{})
	var dcOpenOnce sync.Once
	dc.OnOpen(func() {
		dcOpenOnce.Do(func() { close(dcOpenCh) })
	})

	// DC context — cancelled when DC closes or PC fails.
	dcCtx, dcCancel := context.WithCancel(ctx)
	defer dcCancel()

	dc.OnClose(func() {
		util.Logf("DataChannel 已關閉")
		dcCancel()
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		util.Logf("PeerConnection 狀態: %s", state.String())
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			dcCancel()
		}
	})

	// ── 4. Signaling ───────────────────────────────────────────────────
	if err := doHostSignaling(wsConn, pc, dcOpenCh); err != nil {
		return fmt.Errorf("signaling 失敗: %w", err)
	}

	// WS is now closed. All further communication goes through the DataChannel.
	server.Close() // release listener resources
	util.Logf("WebRTC DataChannel 已建立，WS 已關閉")
	fmt.Println("✓ P2P 隧道已建立！正在轉發流量...")

	// ── 5. Backpressure setup ──────────────────────────────────────────
	sendReady := make(chan struct{}, 1)
	dc.SetBufferedAmountLowThreshold(uint64(tunnel.LowWaterMark))
	dc.OnBufferedAmountLow(func() {
		select {
		case sendReady <- struct{}{}:
		default:
		}
	})

	// ── 6. Dispatcher (host mode) ──────────────────────────────────────
	dispatcher := tunnel.NewDispatcher()

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		pkt, err := protocol.Decode(msg.Data)
		if err != nil {
			util.Logf("封包解碼失敗: %v", err)
			return
		}

		ch, exists := dispatcher.Route(pkt.SocketID)
		if !exists {
			if pkt.Type == protocol.TypeClose {
				return // ignore CLOSE for unknown socketID
			}
			ch, _ = dispatcher.GetOrCreate(pkt.SocketID)
			go tunnel.HostSocketHandler(dcCtx, pkt.SocketID, ch, dc, targetAddr, sendReady, dispatcher.Unregister)
		}

		select {
		case ch <- pkt:
		default:
			util.Logf("[%08x] inbox 已滿，丟棄封包", pkt.SocketID)
		}
	})

	// ── 7. Block until shutdown ────────────────────────────────────────
	<-dcCtx.Done()
	fmt.Println("隧道已關閉")
	return nil
}

// doHostSignaling performs the SDP/ICE exchange on the host side.
// It sends an offer, receives the answer and ICE candidates via WebSocket,
// and returns when the DataChannel opens.
func doHostSignaling(wsConn *websocket.Conn, pc *webrtc.PeerConnection, dcOpenCh <-chan struct{}) error {
	var wsMu sync.Mutex
	wsSend := func(msg signaling.Message) {
		wsMu.Lock()
		defer wsMu.Unlock()
		if err := wsConn.WriteJSON(msg); err != nil {
			util.Logf("WS 發送失敗: %v", err)
		}
	}

	// Trickle ICE candidates.
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		data, _ := json.Marshal(c.ToJSON())
		wsSend(signaling.Message{
			Type:      signaling.MsgTypeCandidate,
			Candidate: string(data),
		})
	})

	// Create and send offer.
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("CreateOffer: %w", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("SetLocalDescription: %w", err)
	}
	wsSend(signaling.Message{Type: signaling.MsgTypeOffer, SDP: offer.SDP})

	// Read loop: answer + ICE candidates.
	errCh := make(chan error, 1)
	go func() {
		for {
			var msg signaling.Message
			if err := wsConn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}
			switch msg.Type {
			case signaling.MsgTypeAnswer:
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  msg.SDP,
				}); err != nil {
					util.Logf("SetRemoteDescription 失敗: %v", err)
				}
			case signaling.MsgTypeCandidate:
				var init webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(msg.Candidate), &init); err == nil {
					if err := pc.AddICECandidate(init); err != nil {
						util.Logf("AddICECandidate 失敗: %v", err)
					}
				}
			}
		}
	}()

	// Wait for DataChannel to open, then close WS.
	select {
	case <-dcOpenCh:
		wsConn.Close()
		return nil
	case err := <-errCh:
		// If WS closed because dcOpenCh already fired, that's fine.
		select {
		case <-dcOpenCh:
			return nil
		default:
			return fmt.Errorf("WS 讀取錯誤: %w", err)
		}
	}
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
