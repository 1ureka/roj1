// Package app contains the top-level orchestration for host and client roles.
package app

import (
	"context"
	"fmt"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/signaling"
	"github.com/1ureka/1ureka.net.p2p/internal/tunnel"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// RunHost orchestrates the full host lifecycle:
//  1. Perform signaling to establish P2P tunnel
//  2. Set up dispatcher
//  3. Forward traffic until shutdown
func RunHost(ctx context.Context, targetPort int) error {
	// ── 1. Signaling ───────────────────────────────────────────────────
	tr, err := signaling.EstablishAsHost(ctx)
	if err != nil {
		return err
	}
	defer tr.Close()

	targetAddr := fmt.Sprintf("127.0.0.1:%d", targetPort)
	fmt.Println("✓ P2P 隧道已建立！正在轉發流量...")

	// ── 2. Dispatcher (host mode) ──────────────────────────────────────
	dispatcher := tunnel.NewDispatcher()

	tr.OnPacket(func(pkt *protocol.Packet, err error) {
		if err != nil {
			util.Logf("封包解碼失敗: %v", err)
			return
		}

		ch, exists := dispatcher.Route(pkt.SocketID)
		if !exists {
			if pkt.Type == protocol.TypeClose {
				return // ignore CLOSE for unknown socketID
			}
			ch, _ = dispatcher.GetOrCreate(pkt.SocketID)
			go tunnel.HostSocketHandler(ctx, pkt.SocketID, ch, tr, targetAddr, dispatcher.Unregister)
		}

		select {
		case ch <- pkt:
		default:
			util.Logf("[%08x] inbox 已滿，丟棄封包", pkt.SocketID)
		}
	})

	// ── 3. Block until shutdown ────────────────────────────────────────
	<-tr.Done()
	fmt.Println("隧道已關閉")
	return nil
}
