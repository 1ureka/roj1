// Package protocol defines the packet format and types for the P2P tunnel.
package protocol

// Packet type constants.
const (
	TypeConnect uint8 = 0x01 // New TCP connection request
	TypeData    uint8 = 0x02 // TCP data payload
	TypeClose   uint8 = 0x03 // Connection close notification
)

// HeaderSize is the fixed header size: Type(1) + SocketID(4) + SeqNum(4).
const HeaderSize = 9

// Packet represents a tunnel protocol packet transmitted over the DataChannel.
type Packet struct {
	Type     uint8  // TypeConnect, TypeData, or TypeClose
	SocketID uint32 // Hashed identifier from 4-tuple
	SeqNum   uint32 // Per-socketID sequence number
	Payload  []byte // Only used for TypeData
}
