// Package adapter manages the post-transport lifecycle of a P2P tunnel.
// Given a ready Transport, it handles packet dispatch, per-socketID goroutine
// management, and TCP bridging for both host and client roles.
package adapter

import (
	"context"
	"net"
	"sync"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// Transport defines the capabilities that adapter requires from the
// underlying data transport layer.
type Transport interface {
	SendConnect(socketID, seqNum uint32)
	SendData(socketID, seqNum uint32, payload []byte)
	SendClose(socketID, seqNum uint32)
	OnPacket(fn func(*protocol.Packet))
	Done() <-chan struct{}
}

// adapter manages the socketID route table and auto-cleanup.
// It is unexported — callers use RunAsHost / RunAsClient.
type adapter struct {
	ctx context.Context
	tr  Transport

	mu     sync.Mutex
	routes map[uint32]*Socket
}

// newAdapter creates an empty adapter bound to the given context and transport.
func newAdapter(ctx context.Context, tr Transport) *adapter {
	return &adapter{
		ctx:    ctx,
		tr:     tr,
		routes: make(map[uint32]*Socket),
	}
}

// registerOrGet (for host) looks up the socketID in the route table. If found, returns the
// existing Socket and false. If not found, creates a new Socket, registers it, and returns it with true.
func (a *adapter) registerOrGet(ctx context.Context, id uint32, tr Transport) (*Socket, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if s, ok := a.routes[id]; ok {
		return s, false
	}

	s := newSocket(ctx, id, tr)
	a.routes[id] = s
	util.Stats.AddConn()

	go func() {
		<-s.ctx.Done()
		a.mu.Lock()
		delete(a.routes, s.id)
		a.mu.Unlock()
		util.Stats.RemoveConn()
	}()

	return s, true
}

// register (for client) adds a socket to the route table and starts an auto-cleanup
// goroutine that removes the entry when the socket's context is done.
func (a *adapter) register(ctx context.Context, id uint32, tr Transport, conn net.Conn) *Socket {
	s := newSocketWithConn(ctx, id, tr, conn)
	a.mu.Lock()
	a.routes[id] = s
	a.mu.Unlock()
	util.Stats.AddConn()

	go func() {
		<-s.ctx.Done()
		a.mu.Lock()
		delete(a.routes, s.id)
		a.mu.Unlock()
		util.Stats.RemoveConn()
	}()

	return s
}

// deliver routes a packet to the matching socket's inbox.
// Returns true if a route was found.
func (a *adapter) deliver(pkt *protocol.Packet) bool {
	a.mu.Lock()
	s, ok := a.routes[pkt.SocketID]
	a.mu.Unlock()

	if !ok {
		return false
	}

	select {
	case s.inbox <- pkt:
	default:
		util.LogWarning("[%08x] inbox full, dropping packet", pkt.SocketID)
	}
	return true
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// RunAsHost starts the host-side adapter. It listens on the DataChannel for
// incoming packets; when an unknown socketID appears (with a non-CLOSE packet),
// it creates a Socket and launches a goroutine that dials targetAddr.
// Blocks until the transport is done.
func RunAsHost(ctx context.Context, tr Transport, targetAddr string) error {
	a := newAdapter(ctx, tr)

	tr.OnPacket(func(pkt *protocol.Packet) {
		if a.deliver(pkt) {
			return
		}
		// Unknown socketID — create a new socket (unless it's a stale CLOSE).
		if pkt.Type == protocol.TypeClose {
			return
		}

		s, created := a.registerOrGet(ctx, pkt.SocketID, tr)
		if created {
			util.LogDebug("[%08x] new socket created for incoming connection", pkt.SocketID)
			go s.runAsHost(targetAddr)
		}

		if !a.deliver(pkt) {
			util.LogError("[%08x] failed to deliver packet to newly created socket", pkt.SocketID)
		}
	})

	<-tr.Done()
	return nil
}

// portToID converts a 16-bit ephemeral port number to a 32-bit socket identifier.
//
// On the client side, the TCP listener binds to a fixed address (e.g., 127.0.0.1:8080).
// Each incoming connection has:
//   - Fixed local IP and port (the virtual service listener address)
//   - Fixed remote IP (loopback, because virtual service listener only accepts local connections)
//   - OS-assigned ephemeral remote port (the only varying component)
//
// Therefore, the remote port alone is sufficient as a unique identifier for each socket.
//
// This function applies a simple XOR-based mixing for visual obfuscation,
// transforming the 16-bit port into a 32-bit value. The transformation is
// deterministic and collision-free within the port space.
func portToID(port uint16) uint32 {
	v := uint32(port)
	v ^= v << 13
	v ^= v >> 17
	v ^= v << 5
	return v
}

// RunAsClient starts the client-side adapter. It listens on localPort for
// incoming TCP connections; each accepted connection becomes a Socket that
// sends CONNECT and bridges data through the DataChannel.
// Blocks until the transport is done.
func RunAsClient(ctx context.Context, tr Transport, localAddr string) error {
	a := newAdapter(ctx, tr)

	// Wire up DataChannel → Socket dispatch.
	tr.OnPacket(func(pkt *protocol.Packet) {
		if a.deliver(pkt) {
			return
		}

		if pkt.Type == protocol.TypeData {
			util.LogDebug("[%08x] unknown socketID, dropping DATA packet", pkt.SocketID)
		}
	})

	// Start TCP listener.
	listener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	util.LogSuccess("virtual service started, listening on %s", localAddr)

	// Accept loop in a separate goroutine so we can also wait on tr.Done().
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					util.LogDebug("virtual service listener closed, stopping accept loop")
				default:
					util.LogError("virtual service accept error: %v", err)
				}
				return
			}

			addr := conn.RemoteAddr().(*net.TCPAddr)
			socketID := portToID(uint16(addr.Port))
			util.LogDebug("[%08x] new connection from %s", socketID, conn.RemoteAddr())

			s := a.register(ctx, socketID, tr, conn)
			go s.runAsClient()
		}
	}()

	<-tr.Done()
	return nil
}
