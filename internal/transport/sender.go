package transport

import (
	"context"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
	"github.com/pion/webrtc/v4"
)

const (
	highWaterMark  = 256 * 1024 // pause sending when bufferedAmount exceeds this
	lowWaterMark   = 64 * 1024  // resume sending when bufferedAmount drops below this
	sendBufferSize = 64         // outgoing packet channel capacity
)

// sender is a goroutine-based packet writer that serializes all writes to a
// single DataChannel, adding open-gate and backpressure control.
type sender struct {
	inbox       chan *protocol.Packet
	drainSignal chan struct{}
}

// newSender creates a sender, wires the backpressure callbacks on dc, and
// starts the background loop. The loop exits when ctx is cancelled.
func newSender(ctx context.Context, dc *webrtc.DataChannel, openSignal <-chan struct{}) *sender {
	s := &sender{
		inbox:       make(chan *protocol.Packet, sendBufferSize),
		drainSignal: make(chan struct{}, 1),
	}

	dc.SetBufferedAmountLowThreshold(uint64(lowWaterMark))
	dc.OnBufferedAmountLow(func() {
		select {
		case s.drainSignal <- struct{}{}:
		default:
		}
	})

	go s.loop(ctx, dc, openSignal)

	return s
}

// loop is the single-writer goroutine. It waits for the DataChannel to open,
// then drains the inbox with backpressure awareness.
func (s *sender) loop(ctx context.Context, dc *webrtc.DataChannel, openSignal <-chan struct{}) {
	// Phase 1: wait for DC to be open.
	select {
	case <-openSignal:
	case <-ctx.Done():
		return
	}

	// Phase 2: send packets with backpressure.
	for {
		select {
		case pkt := <-s.inbox:
			if dc.BufferedAmount() > uint64(highWaterMark) {
				select {
				case <-s.drainSignal:
				case <-ctx.Done():
					return
				}
			}

			data := protocol.Encode(pkt)
			if err := dc.Send(data); err != nil {
				util.LogError("failed to send packet (socketID=%08x, type=%d): %v", pkt.SocketID, pkt.Type, err)
				return
			}

			util.Stats.AddSent(len(data))
		case <-ctx.Done():
			return
		}
	}
}

// send enqueues a packet for transmission. It blocks if the internal buffer
// is full and returns silently when ctx is already cancelled.
func (s *sender) send(ctx context.Context, pkt *protocol.Packet) {
	select {
	case s.inbox <- pkt:
	case <-ctx.Done():
	}
}
