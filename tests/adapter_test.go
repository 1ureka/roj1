package tests

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/1ureka/roj1/internal/adapter"
	"github.com/1ureka/roj1/internal/protocol"
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

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// startEchoServer starts a TCP echo server that copies everything it receives
// back to the sender. Returns the address (host:port) it is listening on.
func startEchoServer(t *testing.T, ctx context.Context) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo server: listen failed: %v", err)
	}
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()
	return l.Addr().String()
}

// getFreeAddr finds a free TCP port on loopback and returns its address.
func getFreeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getFreeAddr: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// waitForListener polls the given address until a TCP connection succeeds or
// the timeout elapses. The probe connection is immediately closed.
func waitForListener(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("listener at %s not ready within %v", addr, timeout)
}

// makeTestData generates deterministic test data of the given size.
// Each byte is derived from its index XOR-ed with the seed, ensuring that
// different connections produce distinguishable payloads.
func makeTestData(size int, seed byte) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i%251) ^ seed
	}
	return data
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunAsHostAndClient exercises the full tunnel path:
//
//	[TCP client] <-> [RunAsClient] <-> [mockTransport] <-> [RunAsHost] <-> [echo server]
//
// Multiple concurrent connections each send a 64 KB payload (which exceeds
// maxPayloadSize = 16 KB, forcing multi-packet splitting). The mockTransport
// delivers packets with random delays, so the Reassembler's out-of-order
// handling is exercised. Data integrity is verified by comparing the echoed
// bytes to the original.
func TestRunAsHostAndClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	// 1. Infrastructure
	echoAddr := startEchoServer(t, ctx)
	clientTr, hostTr := MockTransports()
	clientAddr := getFreeAddr(t)

	var wg sync.WaitGroup
	defer func() {
		cancel()
		clientTr.Close()
		hostTr.Close()
		wg.Wait()
	}()

	// 2. Start both sides of the tunnel
	wg.Add(2)
	go func() {
		defer wg.Done()
		adapter.RunAsHost(ctx, hostTr, echoAddr)
	}()
	go func() {
		defer wg.Done()
		adapter.RunAsClient(ctx, clientTr, clientAddr)
	}()

	waitForListener(t, clientAddr, 5*time.Second)

	// 3. Open multiple TCP connections through the tunnel concurrently
	const numConns = 10
	const dataSize = 10 * 1024 * 1024 // 10 MiB — stress test for multi-packet handling and reassembly

	var connWg sync.WaitGroup
	for i := range numConns {
		connWg.Add(1)
		go func(idx int) {
			defer connWg.Done()

			conn, err := net.Dial("tcp", clientAddr)
			if err != nil {
				t.Errorf("[conn %d] dial: %v", idx, err)
				return
			}
			defer conn.Close()

			sent := makeTestData(dataSize, byte(idx))

			// Write and read concurrently to avoid TCP buffer deadlock.
			errCh := make(chan error, 1)
			go func() {
				_, err := conn.Write(sent)
				errCh <- err
			}()

			got := make([]byte, dataSize)
			conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Errorf("[conn %d] read echo: %v", idx, err)
				return
			}

			if err := <-errCh; err != nil {
				t.Errorf("[conn %d] write: %v", idx, err)
				return
			}

			if !bytes.Equal(sent, got) {
				t.Errorf("[conn %d] echoed data mismatch (sent %d bytes, got %d bytes)",
					idx, len(sent), len(got))
			}
		}(i)
	}

	connWg.Wait()
}
