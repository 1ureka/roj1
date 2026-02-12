package adapter

import "sync/atomic"

// SeqGen is a per-socketID atomic sequence number generator.
// It is shared between the main handler goroutine and the TCP→DC goroutine,
// so all operations are atomic.
type SeqGen struct {
	val atomic.Uint32
}

// NewSeqGen creates a new sequence generator starting at 0.
// The first call to Next() returns 1.
func NewSeqGen() *SeqGen {
	return &SeqGen{}
}

// Next returns the next sequence number (monotonically increasing from 1).
func (s *SeqGen) Next() uint32 {
	return s.val.Add(1)
}
