// Package adapter manages the post-transport lifecycle of a P2P tunnel.
// Given a ready Transport, it handles packet dispatch, per-socketID goroutine
// management, and TCP bridging for both host and client roles.
package adapter

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// adapter manages the socketID route table and auto-cleanup.
// It is unexported — callers use RunAsHost / RunAsClient.
type adapter struct {
	ctx context.Context
	tr  *transport.Transport

	mu     sync.Mutex
	routes map[uint32]*Socket
}

// newAdapter creates an empty adapter bound to the given context and transport.
func newAdapter(ctx context.Context, tr *transport.Transport) *adapter {
	return &adapter{
		ctx:    ctx,
		tr:     tr,
		routes: make(map[uint32]*Socket),
	}
}

// register adds a socket to the route table and starts an auto-cleanup
// goroutine that removes the entry when the socket's context is done.
func (a *adapter) register(s *Socket) {
	a.mu.Lock()
	a.routes[s.id] = s
	a.mu.Unlock()

	go func() {
		<-s.ctx.Done()
		a.mu.Lock()
		delete(a.routes, s.id)
		a.mu.Unlock()
	}()
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
		util.Logf("[%08x] inbox 已滿，丟棄封包", pkt.SocketID)
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
func RunAsHost(ctx context.Context, tr *transport.Transport, targetAddr string) error {
	a := newAdapter(ctx, tr)

	tr.OnPacket(func(pkt *protocol.Packet, err error) {
		if err != nil {
			util.Logf("封包解碼失敗: %v", err)
			return
		}

		if !a.deliver(pkt) {
			// Unknown socketID — create a new socket (unless it's a stale CLOSE).
			if pkt.Type == protocol.TypeClose {
				return
			}

			s := newSocket(ctx, pkt.SocketID, tr)
			a.register(s)
			go s.runAsHost(targetAddr)

			// Deliver the first packet that triggered creation.
			a.deliver(pkt)
		}
	})

	<-tr.Done()
	return nil
}

// RunAsClient starts the client-side adapter. It listens on localPort for
// incoming TCP connections; each accepted connection becomes a Socket that
// sends CONNECT and bridges data through the DataChannel.
// Blocks until the transport is done.
func RunAsClient(ctx context.Context, tr *transport.Transport, localPort int) error {
	a := newAdapter(ctx, tr)

	// Wire up DataChannel → Socket dispatch.
	tr.OnPacket(func(pkt *protocol.Packet, err error) {
		if err != nil {
			util.Logf("封包解碼失敗: %v", err)
			return
		}

		if !a.deliver(pkt) {
			util.Logf("[%08x] 未知 socketID，丟棄封包", pkt.SocketID)
		}
	})

	// Start TCP listener.
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	util.Logf("虛擬服務已啟動，監聽 %s", addr)

	// Accept loop in a separate goroutine so we can also wait on tr.Done().
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					util.Logf("accept error: %v", err)
					return
				}
			}

			socketID := util.SocketIDFromConn(conn)
			util.Logf("[%08x] 新連線 from %s", socketID, conn.RemoteAddr())

			s := newSocketWithConn(ctx, socketID, tr, conn)
			a.register(s)
			go s.runAsClient()
		}
	}()

	<-tr.Done()
	return nil
}
