package tunnel

import (
	"sync"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
)

// Dispatcher maintains the socketID → inbox-channel route table.
// The DataChannel OnMessage callback uses it to route incoming packets
// to the correct per-socketID goroutine.
type Dispatcher struct {
	mu         sync.Mutex
	routeTable map[uint32]chan *protocol.Packet
}

// NewDispatcher creates an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		routeTable: make(map[uint32]chan *protocol.Packet),
	}
}

// Register creates a buffered inbox channel for the given socketID and stores
// it in the route table. Returns the receive end.
func (d *Dispatcher) Register(socketID uint32) <-chan *protocol.Packet {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan *protocol.Packet, InboxBufferSize)
	d.routeTable[socketID] = ch
	return ch
}

// Unregister removes the socketID from the route table.
// The channel is NOT closed — the handler goroutine will exit via ctx.Done().
func (d *Dispatcher) Unregister(socketID uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.routeTable, socketID)
}

// Route looks up the inbox channel for a socketID.
func (d *Dispatcher) Route(socketID uint32) (chan *protocol.Packet, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch, ok := d.routeTable[socketID]
	return ch, ok
}

// GetOrCreate returns the existing channel for a socketID, or creates a new one.
// The boolean return value is true if the channel already existed.
func (d *Dispatcher) GetOrCreate(socketID uint32) (chan *protocol.Packet, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch, exists := d.routeTable[socketID]
	if !exists {
		ch = make(chan *protocol.Packet, InboxBufferSize)
		d.routeTable[socketID] = ch
	}
	return ch, exists
}
