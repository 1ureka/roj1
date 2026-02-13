package signaling

import (
	"sync"

	"github.com/gorilla/websocket"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
)

// ?
type sender struct {
	tr   *transport.Transport
	conn *websocket.Conn
	mu   sync.Mutex
}

// ?
func (s *sender) send(msg message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(msg)
}

// ?
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

// ?
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

// ?
func (s *sender) sendCandidate(candidate string) error {
	return s.send(message{Type: msgTypeCandidate, Candidate: candidate})
}
