package adapter

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/1ureka/roj1/internal/protocol"
	"github.com/1ureka/roj1/internal/util"
)

// Tuning constants.
const (
	maxPayloadSize   = 16 * 1024         // 16 KB per DATA packet payload
	maxBufferedBytes = 500 * 1024 * 1024 // per-socketID reassembler buffer limit (to prevent OOM)
)

// Socket holds the complete lifecycle state for one socketID.
//
// A Socket spawns up to three long-running goroutines (pushLoop,
// writeOrConnLoop/writeLoop, readLoop). Concurrent access is safe because:
//   - inbox is a buffered channel (dispatch → pushLoop)
//   - Reassembler is mutex-protected (pushLoop ↔ writeOrConnLoop/writeLoop)
//   - SeqGen uses atomic operations
//   - tcpConn is set before readLoop is launched (happens-before)
//   - cleanup is guarded by sync.Once
type Socket struct {
	// Identity
	id uint32

	// Lifecycle
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once

	// Communication
	inbox chan *protocol.Packet
	tr    Transport // shared, thread-safe sender

	// Per-socket local tools
	seq   *SeqGen
	reasm *Reassembler

	// TCP side
	tcpConn net.Conn
}

// newSocket creates a Socket without a TCP connection (used by host mode).
func newSocket(parentCtx context.Context, id uint32, tr Transport) *Socket {
	ctx, cancel := context.WithCancel(parentCtx)
	return &Socket{
		id:     id,
		ctx:    ctx,
		cancel: cancel,
		inbox:  make(chan *protocol.Packet, 1024), // pushLoop must never block, 1024 is for -race testing
		tr:     tr,
		seq:    NewSeqGen(),
		reasm:  NewReassembler(),
	}
}

// newSocketWithConn creates a Socket with an already-established TCP connection
// (used by client mode, where the local TCP accept happens first).
func newSocketWithConn(parentCtx context.Context, id uint32, tr Transport, conn net.Conn) *Socket {
	s := newSocket(parentCtx, id, tr)
	s.tcpConn = conn
	return s
}

// ---------------------------------------------------------------------------
// Run methods — external entry points
// ---------------------------------------------------------------------------

// runAsHost is the complete lifecycle for a host-side socketID.
// It launches pushLoop (inbox → Reassembler) and writeOrConnLoop
// (Reassembler → TCP dial + write) as dedicated goroutines, then blocks
// until the context is cancelled (triggered by any goroutine calling cleanup).
func (s *Socket) runAsHost(targetAddr string) {
	defer s.cleanup()

	go s.pushLoop()
	go s.writeOrConnLoop(targetAddr)

	<-s.ctx.Done()
}

// runAsClient is the complete lifecycle for a client-side socketID.
// Already holds a TCP connection from accept; sends CONNECT immediately,
// then launches pushLoop, writeLoop, and readLoop as dedicated goroutines.
// Blocks until the context is cancelled.
func (s *Socket) runAsClient() {
	defer s.cleanup()

	s.tr.SendConnect(s.id, s.seq.Next())

	go s.pushLoop()
	go s.writeLoop()
	go s.readLoop()

	<-s.ctx.Done()
}

// ---------------------------------------------------------------------------
// Loop methods — long-running goroutines
// ---------------------------------------------------------------------------

// writeOrConnLoop is the host-side drain loop. It waits for Reassembler
// notifications, drains consecutive packets, and handles CONNECT (dial TCP),
// DATA (write to TCP), and CLOSE (shut down). On receiving CONNECT it starts
// readLoop for the reverse direction.
func (s *Socket) writeOrConnLoop(targetAddr string) {
	defer s.cleanup()

	connected := false

	for {
		select {
		case <-s.reasm.Ready():
			for _, d := range s.reasm.Drain() {
				switch d.Type {
				case protocol.TypeConnect:
					if connected {
						continue
					}
					conn, err := net.Dial("tcp", targetAddr)
					if err != nil {
						util.LogWarning("[%08x] TCP dial failed: %v", s.id, err)
						return
					}
					s.tcpConn = conn
					connected = true
					util.LogDebug("[%08x] TCP connected to %s", s.id, targetAddr)
					go s.readLoop()

				case protocol.TypeData:
					if !connected {
						continue
					}
					if _, err := s.tcpConn.Write(d.Payload); err != nil {
						util.LogWarning("[%08x] TCP write error: %v", s.id, err)
						return
					}

				case protocol.TypeClose:
					util.LogDebug("[%08x] received CLOSE", s.id)
					return
				}
			}

		case <-s.ctx.Done():
			return
		}
	}
}

// writeLoop is the client-side drain loop. It waits for Reassembler
// notifications, drains consecutive packets, and writes DATA payloads to the
// TCP connection. Returns on CLOSE or context cancellation.
func (s *Socket) writeLoop() {
	defer s.cleanup()

	for {
		select {
		case <-s.reasm.Ready():
			for _, d := range s.reasm.Drain() {
				switch d.Type {
				case protocol.TypeData:
					if _, err := s.tcpConn.Write(d.Payload); err != nil {
						util.LogWarning("[%08x] TCP write error: %v", s.id, err)
						return
					}
				case protocol.TypeClose:
					util.LogDebug("[%08x] received CLOSE", s.id)
					return
				}
			}

		case <-s.ctx.Done():
			return
		}
	}
}

// pushLoop reads packets from the inbox and pushes them into the Reassembler.
// It runs in a dedicated goroutine so that Push (a fast heap insert) is never
// blocked by TCP writes happening in the drain loop (writeOrConnLoop /
// writeLoop).
func (s *Socket) pushLoop() {
	defer s.cleanup()

	for {
		select {
		case pkt := <-s.inbox:
			if s.reasm.Push(pkt) {
				util.LogWarning("[%08x] reassembler buffer exceeded %d MiB, treating as disconnection",
					s.id, maxBufferedBytes/(1024*1024))
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// readLoop reads from the TCP connection and sends DATA packets through the
// DataChannel. It uses a blocking Read; cleanup() closes the TCP connection
// to unblock it.
func (s *Socket) readLoop() {
	defer s.cleanup()

	buf := make([]byte, maxPayloadSize)
	for {
		n, err := s.tcpConn.Read(buf)

		if n > 0 {
			payload := make([]byte, n)
			copy(payload, buf[:n])
			s.tr.SendData(s.id, s.seq.Next(), payload)
		}

		if err == nil {
			continue
		}

		switch {
		case errors.Is(err, io.EOF):
			return // No need to log EOF — it's a normal shutdown signal.

		default:
			select {
			case <-s.ctx.Done():
				return // Already shutting down — no need to log.
			default:
				util.LogWarning("[%08x] TCP read error: %v", s.id, err)
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

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
		util.LogDebug("[%08x] socket cleanup complete", s.id)
	})
}
