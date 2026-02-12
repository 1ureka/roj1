package signaling

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// hostExchange performs the SDP/ICE exchange on the host side:
//   - Create an Offer and send it via WS
//   - Receive the Answer and ICE candidates
//   - Block until the DataChannel opens or an error occurs
func hostExchange(wsConn *websocket.Conn, tr *transport.Transport) error {
	var wsMu sync.Mutex
	wsSend := func(msg message) {
		wsMu.Lock()
		defer wsMu.Unlock()
		if err := wsConn.WriteJSON(msg); err != nil {
			// If WS closed because tr.Ready() already fired, that's fine.
			select {
			case <-tr.Ready():
			default:
				util.Logf("WS 發送失敗: %v", err)
			}
		}
	}

	// Trickle ICE candidates.
	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		data, _ := json.Marshal(c.ToJSON())
		wsSend(message{
			Type:      msgTypeCandidate,
			Candidate: string(data),
		})
	})

	// Create and send offer.
	offer, err := tr.CreateOffer()
	if err != nil {
		return fmt.Errorf("CreateOffer: %w", err)
	}
	if err := tr.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("SetLocalDescription: %w", err)
	}
	wsSend(message{Type: msgTypeOffer, SDP: offer.SDP})

	// Read loop: answer + ICE candidates.
	errCh := make(chan error, 1)
	go func() {
		for {
			var msg message
			if err := wsConn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}
			switch msg.Type {
			case msgTypeAnswer:
				if err := tr.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeAnswer,
					SDP:  msg.SDP,
				}); err != nil {
					util.Logf("SetRemoteDescription 失敗: %v", err)
				}
			case msgTypeCandidate:
				var init webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(msg.Candidate), &init); err == nil {
					if err := tr.AddICECandidate(init); err != nil {
						util.Logf("AddICECandidate 失敗: %v", err)
					}
				}
			}
		}
	}()

	// Wait for DataChannel to open, then close WS.
	select {
	case <-tr.Ready():
		wsConn.Close()
		return nil
	case err := <-errCh:
		// If WS closed because tr.Ready() already fired, that's fine.
		select {
		case <-tr.Ready():
			return nil
		default:
			return fmt.Errorf("WS 讀取錯誤: %w", err)
		}
	}
}

// clientExchange performs the SDP/ICE exchange on the client side:
//   - Receive the Offer
//   - Create an Answer and send it via WS
//   - Exchange ICE candidates
//   - Block until the DataChannel opens or an error occurs
func clientExchange(wsConn *websocket.Conn, tr *transport.Transport) error {
	var wsMu sync.Mutex
	wsSend := func(msg message) {
		wsMu.Lock()
		defer wsMu.Unlock()
		if err := wsConn.WriteJSON(msg); err != nil {
			// If WS closed because tr.Ready() already fired, that's fine.
			select {
			case <-tr.Ready():
			default:
				util.Logf("WS 發送失敗: %v", err)
			}
		}
	}

	// Trickle ICE candidates.
	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		data, _ := json.Marshal(c.ToJSON())
		wsSend(message{
			Type:      msgTypeCandidate,
			Candidate: string(data),
		})
	})

	// Read loop: offer + ICE candidates.
	errCh := make(chan error, 1)
	go func() {
		for {
			var msg message
			if err := wsConn.ReadJSON(&msg); err != nil {
				errCh <- err
				return
			}
			switch msg.Type {
			case msgTypeOffer:
				if err := tr.SetRemoteDescription(webrtc.SessionDescription{
					Type: webrtc.SDPTypeOffer,
					SDP:  msg.SDP,
				}); err != nil {
					util.Logf("SetRemoteDescription 失敗: %v", err)
					continue
				}
				answer, err := tr.CreateAnswer()
				if err != nil {
					util.Logf("CreateAnswer 失敗: %v", err)
					continue
				}
				if err := tr.SetLocalDescription(answer); err != nil {
					util.Logf("SetLocalDescription 失敗: %v", err)
					continue
				}
				wsSend(message{Type: msgTypeAnswer, SDP: answer.SDP})

			case msgTypeCandidate:
				var init webrtc.ICECandidateInit
				if err := json.Unmarshal([]byte(msg.Candidate), &init); err == nil {
					if err := tr.AddICECandidate(init); err != nil {
						util.Logf("AddICECandidate 失敗: %v", err)
					}
				}
			}
		}
	}()

	// Wait for DataChannel to open, then close WS.
	select {
	case <-tr.Ready():
		wsConn.Close()
		return nil
	case err := <-errCh:
		select {
		case <-tr.Ready():
			return nil
		default:
			return fmt.Errorf("WS 讀取錯誤: %w", err)
		}
	}
}
