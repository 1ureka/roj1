// Package webrtc provides helpers for creating PeerConnections and DataChannels.
package webrtc

import (
	"github.com/pion/webrtc/v4"
)

// STUN servers for ICE candidate gathering. No TURN — the tool is designed
// for direct P2P connectivity with zero infrastructure cost.
var stunServers = []string{
	"stun:stun.l.google.com:19302",
	"stun:stun1.l.google.com:19302",
}

// NewPeerConnection creates a PeerConnection configured with Google STUN servers.
func NewPeerConnection() (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: stunServers},
		},
	}
	return webrtc.NewPeerConnection(config)
}

// CreateDataChannel creates a single unordered DataChannel on the given PeerConnection.
// Unordered is chosen by design: since CONNECT and DATA are sent almost simultaneously
// from the client, ordering at the SCTP layer does not prevent out-of-order delivery.
// Embracing unordered eliminates head-of-line blocking between different socketIDs.
func CreateDataChannel(pc *webrtc.PeerConnection) (*webrtc.DataChannel, error) {
	ordered := false
	return pc.CreateDataChannel("tunnel", &webrtc.DataChannelInit{
		Ordered: &ordered,
	})
}
