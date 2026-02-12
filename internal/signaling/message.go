package signaling

// messageType identifies the kind of signaling message (private).
type messageType string

const (
	msgTypeOffer     messageType = "offer"
	msgTypeAnswer    messageType = "answer"
	msgTypeCandidate messageType = "candidate"
)

// message is the JSON structure exchanged over the WebSocket during signaling (private).
type message struct {
	Type      messageType `json:"type"`
	SDP       string      `json:"sdp,omitempty"`
	Candidate string      `json:"candidate,omitempty"` // JSON-encoded ICECandidateInit
}
