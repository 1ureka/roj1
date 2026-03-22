// Package signaling orchestrates the complete signaling phase — from user input
// to an established P2P tunnel. All WebSocket and SDP/ICE details are internal;
// callers receive a ready-to-use Transport.
package signaling

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pterm/pterm"

	"github.com/1ureka/roj1/internal/transport"
	"github.com/1ureka/roj1/internal/util"
)

// readyTimeout is the maximum time to wait for the peer's ready signal
// after the local DataChannel is open.
const readyTimeout = 10 * time.Second

// EstablishAsHost executes the full host-side signaling flow:
//  1. Start a WS server on wsAddr (e.g. ":0" for random port)
//  2. Wait for the client to connect
//  3. Create a Transport
//  4. Perform SDP/ICE exchange
//  5. Dual-flag handshake: wait for both sides to confirm DataChannel open
//  6. Close the WS server and connection (resource cleanup)
//  7. Return the ready Transport
func EstablishAsHost(ctx context.Context, wsAddr string) (*transport.Transport, error) {
	// 1. Start WS server.
	spinner, _ := pterm.DefaultSpinner.
		WithRemoveWhenDone(true).
		Start("starting WebSocket signaling server...")

	srv := &server{connCh: make(chan *websocket.Conn, 1)}
	wsPort, err := srv.start(wsAddr)
	if err != nil {
		spinner.Fail("failed to start WebSocket server")
		return nil, err
	}
	defer srv.close()

	spinner.UpdateText(
		fmt.Sprintf("WebSocket server listening on port %d — waiting for client...", wsPort),
	)

	// 2. Wait for client
	wsConn, err := srv.waitForClient(ctx)
	if err != nil {
		spinner.Fail("failed while waiting for client connection")
		return nil, err
	}
	defer wsConn.Close()

	spinner.UpdateText("client connected — negotiating WebRTC...")

	// 3. Create Transport.
	tr, err := transport.NewTransport(ctx)
	if err != nil {
		spinner.Fail("failed to create Transport")
		return nil, err
	}

	// 4. Perform SDP/ICE exchange.
	s := &sender{tr: tr, conn: wsConn}
	r := &receiver{tr: tr, conn: wsConn, sender: s, peerReady: make(chan struct{}, 1)}

	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			data, _ := json.Marshal(c.ToJSON())
			s.sendCandidate(string(data)) // Error intentionally ignored: sendCandidate is best-effort.
		}
	})

	watchErr := make(chan error, 1)
	go func() {
		watchErr <- r.watch()
	}()

	if err := s.sendOffer(); err != nil {
		tr.Close()
		spinner.Fail("failed to send Offer")
		return nil, err
	}

	// 5. Dual-flag handshake: wait for both sides to confirm DataChannel open.
	select {
	case <-tr.Ready():
	case err := <-watchErr:
		tr.Close()
		spinner.Fail("WebRTC negotiation failed")
		return nil, err
	case <-ctx.Done():
		tr.Close()
		spinner.Fail("WebRTC negotiation failed")
		return nil, ctx.Err()
	}

	if err := s.sendReady(); err != nil {
		util.LogDebug("failed to send ready signal: %v", err)
	}

	select {
	case <-r.peerReady:
		util.LogDebug("peer confirmed ready")
	case <-time.After(readyTimeout):
		util.LogDebug("peer ready timeout — proceeding")
	case <-ctx.Done():
		tr.Close()
		spinner.Fail("WebRTC negotiation failed")
		return nil, ctx.Err()
	}

	spinner.Success("WebRTC DataChannel established")
	return tr, nil
}

// EstablishAsClient executes the full client-side signaling flow:
//  1. Connect to the host's WS server
//  2. Create a Transport
//  3. Perform SDP/ICE exchange
//  4. Dual-flag handshake: wait for both sides to confirm DataChannel open
//  5. Close the WS connection (resource cleanup)
//  6. Return the ready Transport
func EstablishAsClient(ctx context.Context, wsURL string) (*transport.Transport, error) {
	// 1. Connect to WS server.
	spinner, _ := pterm.DefaultSpinner.
		WithRemoveWhenDone(true).
		Start("connecting to Host via WebSocket...")

	wsConn, err := connect(ctx, wsURL)
	if err != nil {
		spinner.Fail("failed to connect to WebSocket server")
		return nil, err
	}
	defer wsConn.Close()

	spinner.UpdateText("WebSocket connected — negotiating WebRTC...")

	// 2. Create Transport.
	tr, err := transport.NewTransport(ctx)
	if err != nil {
		spinner.Fail("failed to create Transport")
		return nil, err
	}

	// 3. Perform SDP/ICE exchange.
	s := &sender{tr: tr, conn: wsConn}
	r := &receiver{tr: tr, conn: wsConn, sender: s, peerReady: make(chan struct{}, 1)}

	tr.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			data, _ := json.Marshal(c.ToJSON())
			s.sendCandidate(string(data)) // Error intentionally ignored: sendCandidate is best-effort.
		}
	})

	watchErr := make(chan error, 1)
	go func() {
		watchErr <- r.watch()
	}()

	// 4. Dual-flag handshake: wait for both sides to confirm DataChannel open.
	select {
	case <-tr.Ready():
	case err := <-watchErr:
		tr.Close()
		spinner.Fail("WebRTC negotiation failed")
		return nil, err
	case <-ctx.Done():
		tr.Close()
		spinner.Fail("WebRTC negotiation failed")
		return nil, ctx.Err()
	}

	if err := s.sendReady(); err != nil {
		util.LogDebug("failed to send ready signal: %v", err)
	}

	select {
	case <-r.peerReady:
		util.LogDebug("peer confirmed ready")
	case <-time.After(readyTimeout):
		util.LogDebug("peer ready timeout — proceeding")
	case <-ctx.Done():
		tr.Close()
		spinner.Fail("WebRTC negotiation failed")
		return nil, ctx.Err()
	}

	spinner.Success("WebRTC DataChannel established")
	return tr, nil
}
