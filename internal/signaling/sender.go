package signaling

import (
	"sync"

	"github.com/gorilla/websocket"

	"github.com/1ureka/roj1/internal/transport"
)

// sender serializes outgoing signaling messages to the WebSocket (private).
type sender struct {
	tr   *transport.Transport
	conn *websocket.Conn
	mu   sync.Mutex
}

// send writes a signaling message to the WebSocket, guarded by a mutex.
func (s *sender) send(msg message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(msg)
}

// sendOffer creates an SDP offer, sets it as local description, and sends it.
func (s *sender) sendOffer() error {
	offer, err := s.tr.CreateOffer()
	if err != nil {
		return err
	}

	if err := s.tr.SetLocalDescription(offer); err != nil {
		return err
	}

	return s.send(message{Type: msgTypeOffer, SDP: offer.SDP})
}

// sendAnswer creates an SDP answer, sets it as local description, and sends it.
func (s *sender) sendAnswer() error {
	answer, err := s.tr.CreateAnswer()
	if err != nil {
		return err
	}

	if err := s.tr.SetLocalDescription(answer); err != nil {
		return err
	}

	return s.send(message{Type: msgTypeAnswer, SDP: answer.SDP})
}

// sendCandidate sends an ICE candidate message over the WebSocket.
func (s *sender) sendCandidate(candidate string) error {
	return s.send(message{Type: msgTypeCandidate, Candidate: candidate})
}
