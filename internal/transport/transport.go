package transport

import (
	"context"
	"errors"
	"sync"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
	"github.com/pion/webrtc/v4"
)

// Transport wraps a single PeerConnection + DataChannel pair, providing a
// high-level API for signaling exchange, packet sending with backpressure,
// and packet receiving.
//
// Its lifecycle is governed by the DataChannel state and the context passed
// at construction time. The PeerConnection state is recorded but does not
// drive open/close decisions.
type Transport struct {
	pc *webrtc.PeerConnection
	dc *webrtc.DataChannel

	sender     *sender
	openSignal chan struct{}

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.RWMutex
	pcState webrtc.PeerConnectionState
}

// NewTransport creates a Transport backed by a new PeerConnection and a
// pre-negotiated DataChannel. The caller should perform signaling via the
// exposed methods (CreateOffer / CreateAnswer / …) and then use Send* /
// OnPacket for data transfer.
//
// The Transport is considered alive as long as the DataChannel is open and
// ctx has not been cancelled.
func NewTransport(ctx context.Context) (*Transport, error) {
	pc, err := newPeerConnection()
	if err != nil {
		return nil, err
	}

	dc, err := newDataChannel(pc)
	if err != nil {
		pc.Close()
		return nil, err
	}

	tCtx, tCancel := context.WithCancel(ctx)

	t := &Transport{
		pc:         pc,
		dc:         dc,
		openSignal: make(chan struct{}),
		ctx:        tCtx,
		cancel:     tCancel,
		pcState:    webrtc.PeerConnectionStateNew,
	}

	// DC open gate.
	var openOnce sync.Once
	dc.OnOpen(func() {
		openOnce.Do(func() { close(t.openSignal) })
	})

	// DC close → cancel transport context.
	dc.OnClose(func() {
		util.Logf("DataChannel closed")
		tCancel()
	})

	// Record PC state (informational only).
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		util.Logf("PeerConnection state: %s", state.String())
		t.mu.Lock()
		t.pcState = state
		t.mu.Unlock()
	})

	// Start the sender goroutine.
	t.sender = newSender(tCtx, dc, t.openSignal)

	return t, nil
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// Ready returns a channel that is closed when the DataChannel is open and
// the Transport is ready to send and receive.
func (t *Transport) Ready() <-chan struct{} {
	return t.openSignal
}

// Done returns a channel that is closed when the Transport is shut down
// (DataChannel closed or parent context cancelled).
func (t *Transport) Done() <-chan struct{} {
	return t.ctx.Done()
}

// Close shuts down the DataChannel and PeerConnection.
func (t *Transport) Close() error {
	t.cancel()
	return errors.Join(t.dc.Close(), t.pc.Close())
}

// ConnectionState returns the last observed PeerConnection state.
func (t *Transport) ConnectionState() webrtc.PeerConnectionState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.pcState
}

// ---------------------------------------------------------------------------
// Signaling
// ---------------------------------------------------------------------------

// CreateOffer generates an SDP offer.
func (t *Transport) CreateOffer() (webrtc.SessionDescription, error) {
	return t.pc.CreateOffer(nil)
}

// CreateAnswer generates an SDP answer.
func (t *Transport) CreateAnswer() (webrtc.SessionDescription, error) {
	return t.pc.CreateAnswer(nil)
}

// SetLocalDescription applies the local SDP.
func (t *Transport) SetLocalDescription(sdp webrtc.SessionDescription) error {
	return t.pc.SetLocalDescription(sdp)
}

// SetRemoteDescription applies the remote SDP.
func (t *Transport) SetRemoteDescription(sdp webrtc.SessionDescription) error {
	return t.pc.SetRemoteDescription(sdp)
}

// OnICECandidate registers a callback invoked whenever a new local ICE
// candidate is gathered. A nil candidate signals the end of gathering.
func (t *Transport) OnICECandidate(fn func(*webrtc.ICECandidate)) {
	t.pc.OnICECandidate(fn)
}

// AddICECandidate adds a remote ICE candidate received through signaling.
func (t *Transport) AddICECandidate(candidate webrtc.ICECandidateInit) error {
	return t.pc.AddICECandidate(candidate)
}

// ---------------------------------------------------------------------------
// Data
// ---------------------------------------------------------------------------

// SendConnect enqueues a CONNECT packet for the given socketID.
func (t *Transport) SendConnect(socketID, seqNum uint32) {
	t.sender.send(t.ctx, &protocol.Packet{
		Type:     protocol.TypeConnect,
		SocketID: socketID,
		SeqNum:   seqNum,
	})
}

// SendClose enqueues a CLOSE packet for the given socketID.
func (t *Transport) SendClose(socketID, seqNum uint32) {
	t.sender.send(t.ctx, &protocol.Packet{
		Type:     protocol.TypeClose,
		SocketID: socketID,
		SeqNum:   seqNum,
	})
}

// SendData enqueues a DATA packet with the given payload.
func (t *Transport) SendData(socketID, seqNum uint32, payload []byte) {
	t.sender.send(t.ctx, &protocol.Packet{
		Type:     protocol.TypeData,
		SocketID: socketID,
		SeqNum:   seqNum,
		Payload:  payload,
	})
}

// OnPacket registers a callback invoked for every inbound DataChannel message.
// The callback receives the decoded packet and any decoding error.
func (t *Transport) OnPacket(fn func(*protocol.Packet, error)) {
	t.dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		pkt, err := protocol.Decode(msg.Data)
		fn(pkt, err)
	})
}
