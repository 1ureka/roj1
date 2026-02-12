package transport

import (
	"github.com/pion/webrtc/v4"
)

// STUN servers for ICE candidate gathering. No TURN — the tool is designed
// for direct P2P connectivity with zero infrastructure cost.
var stunServers = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun1.l.google.com:19302",
}

// newPeerConnection creates a PeerConnection configured with Google STUN servers.
func newPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: stunServers},
		},
	}
	return webrtc.NewPeerConnection(config)
}

// newDataChannel creates a pre-negotiated, unordered DataChannel on the given
// PeerConnection. Using negotiated mode (ID 0) allows both sides to create
// the channel independently without relying on OnDataChannel. Unordered mode
// eliminates head-of-line blocking between different socketIDs.
func newDataChannel(pc *webrtc.PeerConnection) (*webrtc.DataChannel, error) {
	ordered := false
	negotiated := true
	id := uint16(0)

	return pc.CreateDataChannel("tunnel", &webrtc.DataChannelInit{
		Ordered:    &ordered,
		Negotiated: &negotiated,
		ID:         &id,
	})
}
