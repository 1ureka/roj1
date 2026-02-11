// Package signaling handles the WebSocket-based signaling phase for SDP/ICE exchange.
package signaling

// MessageType identifies the kind of signaling message.
type MessageType string

const (
	MsgTypeOffer     MessageType = "offer"
	MsgTypeAnswer    MessageType = "answer"
	MsgTypeCandidate MessageType = "candidate"
)

// Message is the JSON structure exchanged over the WebSocket during signaling.
type Message struct {
	Type      MessageType `json:"type"`
	SDP       string      `json:"sdp,omitempty"`
	Candidate string      `json:"candidate,omitempty"` // JSON-encoded ICECandidateInit
}
