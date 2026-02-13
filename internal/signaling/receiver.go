package signaling

import (
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
)

// ?
type receiver struct {
	tr     *transport.Transport
	conn   *websocket.Conn
	sender *sender
}

// ?
func (r *receiver) watch() error {
	for {
		// ?
		var msg message
		if err := r.conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("讀取 WS 訊息失敗: %w", err)
		}

		switch msg.Type {
		// ?
		case msgTypeOffer:
			if err := r.tr.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer, SDP: msg.SDP,
			}); err != nil {
				return err
			}
			if err := r.sender.sendAnswer(); err != nil {
				return err
			}

		// ?
		case msgTypeAnswer:
			if err := r.tr.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP,
			}); err != nil {
				return err
			}

		// ?
		case msgTypeCandidate:
			var init webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.Candidate), &init); err != nil {
				return fmt.Errorf("解析 ICE candidate 失敗: %w", err)
			}
			if err := r.tr.AddICECandidate(init); err != nil {
				return err
			}
		}
	}
}
