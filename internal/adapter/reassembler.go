package adapter

import (
	"container/heap"
	"sync"

	"github.com/1ureka/roj1/internal/protocol"
	"github.com/1ureka/roj1/internal/util"
)

// Reassembler reorders out-of-order packets within a single socketID stream.
// Push and Drain are designed to run in separate goroutines:
//   - Push: called from the inbox-consuming goroutine (fast, mutex-guarded heap insert)
//   - Drain: called from the TCP-writing goroutine (pops consecutive in-order packets)
//
// Ready() returns a channel that signals when drainable packets are available.
type Reassembler struct {
	mu            sync.Mutex
	expectedSeq   uint32
	buffer        packetHeap
	bufferedBytes int
	notify        chan struct{}
}

// NewReassembler creates a reassembler expecting sequence numbers starting at 1.
func NewReassembler() *Reassembler {
	return &Reassembler{
		expectedSeq: 1,
		notify:      make(chan struct{}, 1),
	}
}

// Ready returns a channel that receives a signal whenever one or more packets
// become drainable (i.e. the next expected sequence number is at the front of
// the buffer). The consumer should call Drain after receiving each signal.
func (r *Reassembler) Ready() <-chan struct{} {
	return r.notify
}

// Push inserts a packet into the reorder buffer. It is goroutine-safe and
// designed to be as fast as possible (single mutex-guarded heap push).
// Returns true if the buffer has exceeded the size limit (caller should
// treat this as a fatal condition and tear down the socket).
func (r *Reassembler) Push(pkt *protocol.Packet) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pkt.SeqNum < r.expectedSeq {
		util.LogDebug("[%08x] received packet with old SeqNum %d (expected %d), ignoring",
			pkt.SocketID, pkt.SeqNum, r.expectedSeq)
		return false
	}

	heap.Push(&r.buffer, pkt)
	r.bufferedBytes += len(pkt.Payload)

	overflow := r.bufferedBytes > maxBufferedBytes

	// Notify the drain side if consecutive packets are now available.
	if r.buffer[0].SeqNum == r.expectedSeq {
		select {
		case r.notify <- struct{}{}:
		default: // already notified
		}
	}

	return overflow
}

// Drain returns all consecutive in-order packets starting from the expected
// sequence number. It is goroutine-safe. Returns nil if no packets are
// currently drainable.
func (r *Reassembler) Drain() []*protocol.Packet {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*protocol.Packet
	for r.buffer.Len() > 0 && r.buffer[0].SeqNum == r.expectedSeq {
		popped := heap.Pop(&r.buffer).(*protocol.Packet)
		r.bufferedBytes -= len(popped.Payload)
		result = append(result, popped)
		r.expectedSeq++
	}
	return result
}

// ---------------------------------------------------------------------------
// packetHeap implements a min-heap sorted by SeqNum.
// ---------------------------------------------------------------------------

type packetHeap []*protocol.Packet

func (h packetHeap) Len() int            { return len(h) }
func (h packetHeap) Less(i, j int) bool  { return h[i].SeqNum < h[j].SeqNum }
func (h packetHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *packetHeap) Push(x interface{}) { *h = append(*h, x.(*protocol.Packet)) }

func (h *packetHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	*h = old[:n-1]
	return item
}
