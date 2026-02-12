package tunnel

import (
	"context"
	"net"
	"time"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// Tuning constants.
const (
	MaxPayloadSize  = 16 * 1024              // 16 KB per DATA packet payload
	ReadTimeout     = 100 * time.Millisecond // TCP read deadline for interruptibility
	InboxBufferSize = 64                     // per-socketID inbox channel capacity
)

// ---------------------------------------------------------------------------
// Host-side per-socketID handler
// ---------------------------------------------------------------------------

// HostSocketHandler is the goroutine that manages a single socketID on the host side.
// It processes packets from the inbox channel (delivered by the dispatcher),
// dials the target TCP service on CONNECT, and bridges data bidirectionally.
func HostSocketHandler(
	parentCtx context.Context,
	id uint32,
	inbox <-chan *protocol.Packet,
	tr *transport.Transport,
	targetAddr string,
	unregister func(uint32),
) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	defer unregister(id)

	var tcpConn net.Conn
	var pending []*protocol.Packet
	reasm := NewReassembler()
	seq := NewSeqGen()

	defer func() {
		if tcpConn != nil {
			tcpConn.Close()
		}
	}()

	for {
		select {
		case pkt, ok := <-inbox:
			if !ok {
				return
			}
			delivered := reasm.Feed(pkt)
			for _, d := range delivered {
				switch d.Type {
				case protocol.TypeConnect:
					if tcpConn != nil {
						// Already connected — ignore duplicate CONNECT.
						continue
					}
					conn, err := net.Dial("tcp", targetAddr)
					if err != nil {
						util.Logf("[%08x] TCP dial failed: %v", id, err)
						tr.SendClose(id, seq.Next())
						return
					}
					tcpConn = conn
					util.Logf("[%08x] TCP connected to %s", id, targetAddr)

					// Flush buffered DATA that arrived before CONNECT.
					for _, p := range pending {
						if _, err := tcpConn.Write(p.Payload); err != nil {
							util.Logf("[%08x] TCP write error (flush): %v", id, err)
							tr.SendClose(id, seq.Next())
							return
						}
					}
					pending = nil

					// Start the independent TCP→DataChannel goroutine (Method A — draft5).
					go tcpToDataChannel(ctx, cancel, tcpConn, tr, id, seq)

				case protocol.TypeData:
					if tcpConn == nil {
						// TCP not yet established — buffer for later.
						pending = append(pending, d)
					} else {
						if _, err := tcpConn.Write(d.Payload); err != nil {
							util.Logf("[%08x] TCP write error: %v", id, err)
							tr.SendClose(id, seq.Next())
							return
						}
					}

				case protocol.TypeClose:
					util.Logf("[%08x] received CLOSE", id)
					return
				}
			}

		case <-ctx.Done():
			// Parent cancelled or tcpToDataChannel exited.
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Client-side per-socketID handler
// ---------------------------------------------------------------------------

// ClientSocketHandler is the goroutine that manages a single socketID on the client side.
// It sends CONNECT immediately, then bridges the local TCP connection with the DataChannel.
func ClientSocketHandler(
	parentCtx context.Context,
	id uint32,
	tcpConn net.Conn,
	tr *transport.Transport,
	dispatcher *Dispatcher,
) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	defer tcpConn.Close()
	defer dispatcher.Unregister(id)

	inbox := dispatcher.Register(id)
	reasm := NewReassembler()
	seq := NewSeqGen()

	// Immediately send CONNECT — do not wait for host acknowledgement (by design).
	tr.SendConnect(id, seq.Next())

	// Start the independent TCP→DataChannel goroutine.
	go tcpToDataChannel(ctx, cancel, tcpConn, tr, id, seq)

	// Main loop: DataChannel → TCP direction.
	for {
		select {
		case pkt, ok := <-inbox:
			if !ok {
				return
			}
			delivered := reasm.Feed(pkt)
			for _, d := range delivered {
				switch d.Type {
				case protocol.TypeData:
					if _, err := tcpConn.Write(d.Payload); err != nil {
						util.Logf("[%08x] TCP write error: %v", id, err)
						tr.SendClose(id, seq.Next())
						return
					}
				case protocol.TypeClose:
					util.Logf("[%08x] received CLOSE", id)
					return
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

// ---------------------------------------------------------------------------
// TCP → DataChannel goroutine (shared by host and client)
// ---------------------------------------------------------------------------

// tcpToDataChannel reads from a TCP connection and sends DATA packets through
// the DataChannel. It includes backpressure control: when bufferedAmount exceeds
// HighWaterMark, it blocks until the sendReady signal indicates the buffer has
// drained below LowWaterMark.
//
// On exit (TCP error or context cancellation), it sends a CLOSE packet and
// cancels the shared context so the main handler also exits.
func tcpToDataChannel(
	ctx context.Context,
	cancel context.CancelFunc,
	tcpConn net.Conn,
	tr *transport.Transport,
	socketID uint32,
	seq *SeqGen,
) {
	defer cancel()

	buf := make([]byte, MaxPayloadSize)
	for {
		// Use a short deadline so we can periodically check ctx.Done().
		tcpConn.SetReadDeadline(time.Now().Add(ReadTimeout))
		n, err := tcpConn.Read(buf)

		if n > 0 {
			payload := make([]byte, n)
			copy(payload, buf[:n])
			tr.SendData(socketID, seq.Next(), payload)
		}

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Read timeout — check ctx and retry.
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			// Real TCP error — send CLOSE and exit.
			util.Logf("[%08x] TCP read error: %v", socketID, err)
			tr.SendClose(socketID, seq.Next())
			return
		}
	}
}
