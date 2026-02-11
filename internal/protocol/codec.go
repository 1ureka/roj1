package protocol

import (
	"encoding/binary"
	"fmt"
)

// Encode serializes a Packet into a byte slice for DataChannel transmission.
func Encode(pkt *Packet) []byte {
	size := HeaderSize + len(pkt.Payload)
	buf := make([]byte, size)
	buf[0] = pkt.Type
	binary.BigEndian.PutUint32(buf[1:5], pkt.SocketID)
	binary.BigEndian.PutUint32(buf[5:9], pkt.SeqNum)
	if len(pkt.Payload) > 0 {
		copy(buf[HeaderSize:], pkt.Payload)
	}
	return buf
}

// Decode deserializes a byte slice into a Packet.
func Decode(data []byte) (*Packet, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("packet too short: %d bytes (need at least %d)", len(data), HeaderSize)
	}
	pkt := &Packet{
		Type:     data[0],
		SocketID: binary.BigEndian.Uint32(data[1:5]),
		SeqNum:   binary.BigEndian.Uint32(data[5:9]),
	}
	if len(data) > HeaderSize {
		pkt.Payload = make([]byte, len(data)-HeaderSize)
		copy(pkt.Payload, data[HeaderSize:])
	}
	return pkt, nil
}
