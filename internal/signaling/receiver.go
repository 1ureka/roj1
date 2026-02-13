package signaling

import (
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
)

// receiver processes incoming signaling messages from the WebSocket (private).
type receiver struct {
	tr     *transport.Transport
	conn   *websocket.Conn
	sender *sender
}

// watch reads signaling messages in a loop and applies them to the Transport.
func (r *receiver) watch() error {
	for {
		var msg message
		if err := r.conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("failed to read WS message: %w", err)
		}

		switch msg.Type {
		// Handle offer: set as remote description and respond with an answer.
		case msgTypeOffer:
			if err := r.tr.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer, SDP: msg.SDP,
			}); err != nil {
				return err
			}
			if err := r.sender.sendAnswer(); err != nil {
				return err
			}

		// Handle answer: set as remote description.
		case msgTypeAnswer:
			if err := r.tr.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeAnswer, SDP: msg.SDP,
			}); err != nil {
				return err
			}

		// Handle ICE candidate: add to the PeerConnection.
		case msgTypeCandidate:
			var init webrtc.ICECandidateInit
			if err := json.Unmarshal([]byte(msg.Candidate), &init); err != nil {
				return fmt.Errorf("failed to parse ICE candidate: %w", err)
			}
			if err := r.tr.AddICECandidate(init); err != nil {
				return err
			}
		}
	}
}
