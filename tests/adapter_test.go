package tests

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/1ureka/1ureka.net.p2p/internal/adapter"
	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
)

// Compile-time interface check.
var _ adapter.Transport = (*mockTransport)(nil)

// mockTransport implements adapter.Transport for in-process testing.
// Two linked mockTransport instances simulate a bidirectional network link:
// packets sent by one side are delivered to the other side's OnPacket handler
// after a random delay in [0, 200ms).
type mockTransport struct {
	mu      sync.RWMutex
	handler func(*protocol.Packet)
	peer    *mockTransport
	done    chan struct{}
	once    sync.Once
}

// MockTransports creates a linked pair of mock transports.
// clientTransport and hostTransport each implement adapter.Transport.
// When one side sends a packet, the other side's OnPacket handler is invoked
// after a random delay less than 200ms.
// Call Close() on either transport to signal Done().
func MockTransports() (clientTransport, hostTransport *mockTransport) {
	client := &mockTransport{done: make(chan struct{})}
	host := &mockTransport{done: make(chan struct{})}
	client.peer = host
	host.peer = client
	return client, host
}

// Close signals that this transport is done. Safe to call multiple times.
func (m *mockTransport) Close() {
	m.once.Do(func() { close(m.done) })
}

// Done returns a channel that is closed when Close() is called.
func (m *mockTransport) Done() <-chan struct{} {
	return m.done
}

// OnPacket registers a callback for incoming packets.
// Thread-safe; may be called before or after the peer starts sending.
func (m *mockTransport) OnPacket(fn func(*protocol.Packet)) {
	m.mu.Lock()
	m.handler = fn
	m.mu.Unlock()
}

// SendConnect sends a CONNECT packet to the peer.
func (m *mockTransport) SendConnect(socketID, seqNum uint32) {
	m.deliverToPeer(&protocol.Packet{
		Type:     protocol.TypeConnect,
		SocketID: socketID,
		SeqNum:   seqNum,
	})
}

// SendData sends a DATA packet to the peer.
func (m *mockTransport) SendData(socketID, seqNum uint32, payload []byte) {
	m.deliverToPeer(&protocol.Packet{
		Type:     protocol.TypeData,
		SocketID: socketID,
		SeqNum:   seqNum,
		Payload:  payload,
	})
}

// SendClose sends a CLOSE packet to the peer.
func (m *mockTransport) SendClose(socketID, seqNum uint32) {
	m.deliverToPeer(&protocol.Packet{
		Type:     protocol.TypeClose,
		SocketID: socketID,
		SeqNum:   seqNum,
	})
}

// deliverToPeer schedules asynchronous delivery of a packet to the peer's
// OnPacket handler with a random delay in [0, 200ms).
// If either side is closed before the delay elapses, the packet is silently dropped.
func (m *mockTransport) deliverToPeer(pkt *protocol.Packet) {
	go func() {
		delay := time.Duration(rand.Int64N(200)) * time.Millisecond

		select {
		case <-time.After(delay):
		case <-m.done:
			return
		case <-m.peer.done:
			return
		}

		m.peer.mu.RLock()
		fn := m.peer.handler
		m.peer.mu.RUnlock()

		if fn != nil {
			fn(pkt)
		}
	}()
}
