package adapter

import (
	"context"
	"net"
	"sync"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// Tuning constants.
const (
	maxPayloadSize  = 16 * 1024 // 16 KB per DATA packet payload
	inboxBufferSize = 64        // per-socketID inbox channel capacity
)

// Socket holds the complete lifecycle state for one socketID.
// It is goroutine-local — only the owning goroutine calls its methods.
type Socket struct {
	// Identity
	id uint32

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	// Communication
	inbox chan *protocol.Packet // fed by the adapter's dispatch loop
	tr    *transport.Transport  // shared, thread-safe sender

	// Per-socket local tools
	seq   *SeqGen
	reasm *Reassembler

	// TCP side
	tcpConn net.Conn
}

// newSocket creates a Socket without a TCP connection (used by host mode).
func newSocket(parentCtx context.Context, id uint32, tr *transport.Transport) *Socket {
	ctx, cancel := context.WithCancel(parentCtx)
	return &Socket{
		id:     id,
		ctx:    ctx,
		cancel: cancel,
		inbox:  make(chan *protocol.Packet, inboxBufferSize),
		tr:     tr,
		seq:    NewSeqGen(),
		reasm:  NewReassembler(),
	}
}

// newSocketWithConn creates a Socket with an already-established TCP connection
// (used by client mode, where the local TCP accept happens first).
func newSocketWithConn(parentCtx context.Context, id uint32, tr *transport.Transport, conn net.Conn) *Socket {
	s := newSocket(parentCtx, id, tr)
	s.tcpConn = conn
	return s
}

// ---------------------------------------------------------------------------
// Host-side entry point
// ---------------------------------------------------------------------------

// runAsHost is the complete lifecycle for a host-side socketID.
// Single state machine: waits for CONNECT → dials TCP → bidirectional
// forwarding. All packet types are handled in one for-range, so no data
// is lost when Reassembler delivers CONNECT and DATA in the same batch.
func (s *Socket) runAsHost(targetAddr string) {
	defer s.cleanup()

	connected := false

	for {
		select {
		case pkt := <-s.inbox:
			for _, d := range s.reasm.Feed(pkt) {
				switch d.Type {
				case protocol.TypeConnect:
					if connected {
						continue
					}
					conn, err := net.Dial("tcp", targetAddr)
					if err != nil {
						util.Logf("[%08x] TCP dial failed: %v", s.id, err)
						return
					}
					s.tcpConn = conn
					connected = true
					util.Logf("[%08x] TCP connected to %s", s.id, targetAddr)
					go s.pumpTCPToDataChannel()

				case protocol.TypeData:
					if !connected {
						continue
					}
					if _, err := s.tcpConn.Write(d.Payload); err != nil {
						util.Logf("[%08x] TCP write error: %v", s.id, err)
						return
					}

				case protocol.TypeClose:
					util.Logf("[%08x] received CLOSE", s.id)
					return
				}
			}

		case <-s.ctx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Client-side entry point
// ---------------------------------------------------------------------------

// runAsClient is the complete lifecycle for a client-side socketID.
// Already holds a TCP connection from accept; sends CONNECT immediately,
// then forwards data bidirectionally until CLOSE or context cancellation.
func (s *Socket) runAsClient() {
	defer s.cleanup()

	s.tr.SendConnect(s.id, s.seq.Next())
	go s.pumpTCPToDataChannel()

	for {
		select {
		case pkt := <-s.inbox:
			for _, d := range s.reasm.Feed(pkt) {
				switch d.Type {
				case protocol.TypeData:
					if _, err := s.tcpConn.Write(d.Payload); err != nil {
						util.Logf("[%08x] TCP write error: %v", s.id, err)
						return
					}
				case protocol.TypeClose:
					util.Logf("[%08x] received CLOSE", s.id)
					return
				}
			}

		case <-s.ctx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// TCP → DataChannel
// ---------------------------------------------------------------------------

// pumpTCPToDataChannel reads from the TCP connection and sends DATA packets.
// It uses a blocking Read; cleanup() closes the TCP connection to unblock it.
func (s *Socket) pumpTCPToDataChannel() {
	defer s.cleanup()

	buf := make([]byte, maxPayloadSize)
	for {
		n, err := s.tcpConn.Read(buf)

		if n > 0 {
			payload := make([]byte, n)
			copy(payload, buf[:n])
			s.tr.SendData(s.id, s.seq.Next(), payload)
		}

		if err != nil {
			select {
			case <-s.ctx.Done():
				// Already shutting down — no need to log.
			default:
				util.Logf("[%08x] TCP read error: %v", s.id, err)
			}
			return
		}
	}
}

// cleanup consolidates all shutdown actions behind sync.Once so that
// regardless of which goroutine exits first, resources are released
// exactly once and the peer is notified with a single CLOSE packet.
func (s *Socket) cleanup() {
	s.closeOnce.Do(func() {
		s.cancel()
		if s.tcpConn != nil {
			s.tcpConn.Close()
		}
		s.tr.SendClose(s.id, s.seq.Next())
		util.Logf("[%08x] Socket cleanup complete", s.id)
	})
}
