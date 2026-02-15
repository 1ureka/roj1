package adapter

import (
	"container/heap"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// Reassembler reorders out-of-order packets within a single socketID stream.
// It is goroutine-local (used inside a per-socketID goroutine) and needs no locking.
type Reassembler struct {
	expectedSeq uint32
	buffer      packetHeap
}

// NewReassembler creates a reassembler expecting sequence numbers starting at 1.
func NewReassembler() *Reassembler {
	return &Reassembler{expectedSeq: 1}
}

// Feed processes an incoming packet and returns all packets that can now be
// delivered in sequence order. Returns nil if no packets are ready.
func (r *Reassembler) Feed(pkt *protocol.Packet) []*protocol.Packet {
	if pkt.SeqNum < r.expectedSeq {
		util.LogDebug("[%08x] received packet with old SeqNum %d (expected %d), ignoring",
			pkt.SocketID, pkt.SeqNum, r.expectedSeq)
		return nil
	}

	if pkt.SeqNum > r.expectedSeq {
		// Future packet — buffer it.
		heap.Push(&r.buffer, pkt)
		return nil
	}

	// pkt.SeqNum == r.expectedSeq — deliver it and drain any consecutive buffered packets.
	result := []*protocol.Packet{pkt}
	r.expectedSeq++

	for r.buffer.Len() > 0 && r.buffer[0].SeqNum == r.expectedSeq {
		result = append(result, heap.Pop(&r.buffer).(*protocol.Packet))
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
