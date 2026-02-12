package app

import (
	"context"
	"fmt"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/signaling"
	"github.com/1ureka/1ureka.net.p2p/internal/tunnel"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// RunClient orchestrates the full client lifecycle:
//  1. Perform signaling to establish P2P tunnel
//  2. Set up dispatcher and virtual service listener
//  3. Forward traffic until shutdown
func RunClient(ctx context.Context, wsURL string, localPort int) error {
	// ── 1. Signaling ───────────────────────────────────────────────────
	tr, err := signaling.EstablishAsClient(ctx, wsURL)
	if err != nil {
		return err
	}
	defer tr.Close()

	fmt.Println("✓ P2P 隧道已建立！")

	// ── 2. Dispatcher (client mode) ────────────────────────────────────
	dispatcher := tunnel.NewDispatcher()

	tr.OnPacket(func(pkt *protocol.Packet, err error) {
		if err != nil {
			util.Logf("封包解碼失敗: %v", err)
			return
		}

		ch, ok := dispatcher.Route(pkt.SocketID)
		if !ok {
			// On client side, handlers are created by the listener, not the dispatcher.
			util.Logf("[%08x] 未知 socketID ，丟棄封包", pkt.SocketID)
			return
		}

		select {
		case ch <- pkt:
		default:
			util.Logf("[%08x] inbox 已滿，丟棄封包", pkt.SocketID)
		}
	})

	// ── 3. Virtual service listener ────────────────────────────────────
	go func() {
		if err := tunnel.ListenAndServe(ctx, localPort, tr, dispatcher); err != nil {
			util.Logf("虛擬服務錯誤: %v", err)
		}
	}()

	fmt.Printf("正在監聯 127.0.0.1:%d，流量將透過 P2P 隧道轉發至 Host\n", localPort)

	// ── 4. Block until shutdown ────────────────────────────────────────
	<-tr.Done()
	fmt.Println("隧道已關閉")
	return nil
}
