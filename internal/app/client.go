package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/signaling"
	"github.com/1ureka/1ureka.net.p2p/internal/tunnel"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
	webrtcpkg "github.com/1ureka/1ureka.net.p2p/internal/webrtc"
)

// RunClient orchestrates the full client lifecycle:
//  1. Connect to the host's WS server
//  2. Perform WebRTC signaling (SDP + ICE exchange)
//  3. Close WebSocket once DataChannel opens
//  4. Set up backpressure, dispatcher, and virtual service listener
//  5. Forward traffic until shutdown
func RunClient(ctx context.Context, wsURL string, localPort int) error {
	// ── 1. Connect to WS server ────────────────────────────────────────
	fmt.Println("正在連線到 Host...")
	wsConn, err := signaling.Connect(ctx, wsURL)
	if err != nil {
		return err
	}
	defer wsConn.Close()
	util.Logf("WS 已連線: %s", wsURL)

	// ── 2. Create PeerConnection ───────────────────────────────────────
	pc, err := webrtcpkg.NewPeerConnection()
	if err != nil {
		return fmt.Errorf("建立 PeerConnection 失敗: %w", err)
	}
	defer pc.Close()

	// DC will be provided by the host via OnDataChannel.
	dcCh := make(chan *webrtc.DataChannel, 1)
	dcOpenCh := make(chan struct{})
	var dcOpenOnce sync.Once

	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		d.OnOpen(func() {
			dcOpenOnce.Do(func() { close(dcOpenCh) })
		})
		select {
		case dcCh <- d:
		default:
		}
	})

	// DC context — cancelled when DC closes or PC fails.
	dcCtx, dcCancel := context.WithCancel(ctx)
	defer dcCancel()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		util.Logf("PeerConnection 狀態: %s", state.String())
		switch state {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			dcCancel()
		}
	})

	// ── 3. Signaling ───────────────────────────────────────────────────
	if err := doClientSignaling(wsConn, pc, dcOpenCh); err != nil {
		return fmt.Errorf("signaling 失敗: %w", err)
	}

	// Get the DataChannel reference.
	dc := webrtcpkg.NewDataChannel(<-dcCh)

	dc.OnClose(func() {
		util.Logf("DataChannel 已關閉")
		dcCancel()
	})

	util.Logf("WebRTC DataChannel 已建立，WS 已關閉")
	fmt.Println("✓ P2P 隧道已建立！")

	// ── 5. Dispatcher (client mode) ────────────────────────────────────
	dispatcher := tunnel.NewDispatcher()

	dc.OnPacket(func(pkt *protocol.Packet, err error) {
		if err != nil {
			util.Logf("封包解碼失敗: %v", err)
			return
		}

		ch, ok := dispatcher.Route(pkt.SocketID)
		if !ok {
			// On client side, handlers are created by the listener, not the dispatcher.
			util.Logf("[%08x] 未知 socketID ，丟棄封包", pkt.SocketID)
			return
		}

		select {
		case ch <- pkt:
		default:
			util.Logf("[%08x] inbox 已滿，丟棄封包", pkt.SocketID)
		}
	})

	// ── 6. Virtual service listener ────────────────────────────────────
	go func() {
		if err := tunnel.ListenAndServe(dcCtx, localPort, dc, dispatcher); err != nil {
			util.Logf("虛擬服務錯誤: %v", err)
			dcCancel()
		}
	}()

	fmt.Printf("正在監聽 127.0.0.1:%d，流量將透過 P2P 隧道轉發至 Host\n", localPort)

	// ── 7. Block until shutdown ────────────────────────────────────────
	<-dcCtx.Done()
	fmt.Println("隧道已關閉")
	return nil
}

// doClientSignaling performs the SDP/ICE exchange on the client side.
// It receives the offer, creates and sends an answer, exchanges ICE candidates,
// and returns when the DataChannel opens.
func doClientSignaling(wsConn *websocket.Conn, pc *webrtc.PeerConnection, dcOpenCh <-chan struct{}) error {
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

	// Read loop: offer + ICE candidates.
	errCh := make(chan error, 1)
	go func() {
		for {
			var msg signaling.Message
			if err := wsConn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}
			switch msg.Type {
			case signaling.MsgTypeOffer:
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  msg.SDP,
				}); err != nil {
					util.Logf("SetRemoteDescription 失敗: %v", err)
					continue
				}
				answer, err := pc.CreateAnswer(nil)
				if err != nil {
					util.Logf("CreateAnswer 失敗: %v", err)
					continue
				}
				if err := pc.SetLocalDescription(answer); err != nil {
					util.Logf("SetLocalDescription 失敗: %v", err)
					continue
				}
				wsSend(signaling.Message{Type: signaling.MsgTypeAnswer, SDP: answer.SDP})

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
		select {
		case <-dcOpenCh:
			return nil
		default:
			return fmt.Errorf("WS 讀取錯誤: %w", err)
		}
	}
}
