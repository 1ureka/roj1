package tests

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
)

// TestEncodeDecodeRoundTrip verifies that encoding and decoding are inverse operations
// for all packet types with various payload sizes.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	testCases := []struct {
		name string
		pkt  *protocol.Packet
	}{
		{
			name: "TypeConnect with no payload",
			pkt: &protocol.Packet{
				Type:     protocol.TypeConnect,
				SocketID: 0x12345678,
				SeqNum:   1,
				Payload:  nil,
			},
		},
		{
			name: "TypeData with small payload",
			pkt: &protocol.Packet{
				Type:     protocol.TypeData,
				SocketID: 0xDEADBEEF,
				SeqNum:   42,
				Payload:  []byte("hello world"),
			},
		},
		{
			name: "TypeClose with no payload",
			pkt: &protocol.Packet{
				Type:     protocol.TypeClose,
				SocketID: 0xCAFEBABE,
				SeqNum:   100,
				Payload:  nil,
			},
		},
		{
			name: "TypeData with large payload (16KB)",
			pkt: &protocol.Packet{
				Type:     protocol.TypeData,
				SocketID: 0x11223344,
				SeqNum:   999,
				Payload:  make([]byte, 16*1024),
			},
		},
		{
			name: "TypeData with empty payload",
			pkt: &protocol.Packet{
				Type:     protocol.TypeData,
				SocketID: 0xAABBCCDD,
				SeqNum:   555,
				Payload:  []byte{},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			encoded := protocol.Encode(tc.pkt)

			// Decode
			decoded, err := protocol.Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			// Verify fields
			if decoded.Type != tc.pkt.Type {
				t.Errorf("Type mismatch: got %d, want %d", decoded.Type, tc.pkt.Type)
			}
			if decoded.SocketID != tc.pkt.SocketID {
				t.Errorf("SocketID mismatch: got 0x%08X, want 0x%08X", decoded.SocketID, tc.pkt.SocketID)
			}
			if decoded.SeqNum != tc.pkt.SeqNum {
				t.Errorf("SeqNum mismatch: got %d, want %d", decoded.SeqNum, tc.pkt.SeqNum)
			}
			if !bytes.Equal(decoded.Payload, tc.pkt.Payload) {
				t.Errorf("Payload mismatch: got %v, want %v", decoded.Payload, tc.pkt.Payload)
			}
		})
	}
}

// TestDecodeTooShort verifies that Decode returns an error when the input
// is shorter than HeaderSize.
func TestDecodeTooShort(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"1 byte", []byte{0x01}},
		{"8 bytes (one less than HeaderSize)", make([]byte, 8)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := protocol.Decode(tc.data)
			if err == nil {
				t.Fatal("Expected error for short packet, got nil")
			}
		})
	}
}

// TestDecodeExactHeaderSize verifies that a packet with exactly HeaderSize
// bytes (no payload) is decoded successfully.
func TestDecodeExactHeaderSize(t *testing.T) {
	original := &protocol.Packet{
		Type:     protocol.TypeConnect,
		SocketID: 0xABCDEF01,
		SeqNum:   777,
		Payload:  nil,
	}

	encoded := protocol.Encode(original)
	if len(encoded) != protocol.HeaderSize {
		t.Fatalf("Expected encoded size to be %d, got %d", protocol.HeaderSize, len(encoded))
	}

	decoded, err := protocol.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	if decoded.Type != original.Type ||
		decoded.SocketID != original.SocketID ||
		decoded.SeqNum != original.SeqNum ||
		len(decoded.Payload) != 0 {
		t.Errorf("Decoded packet mismatch: %+v", decoded)
	}
}

// TestEncodeBoundaryValues tests encoding and decoding with boundary values
// for SocketID and SeqNum.
func TestEncodeBoundaryValues(t *testing.T) {
	testCases := []struct {
		name     string
		socketID uint32
		seqNum   uint32
	}{
		{"zero values", 0, 0},
		{"max SocketID", 0xFFFFFFFF, 123},
		{"max SeqNum", 456, 0xFFFFFFFF},
		{"both max", 0xFFFFFFFF, 0xFFFFFFFF},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			original := &protocol.Packet{
				Type:     protocol.TypeData,
				SocketID: tc.socketID,
				SeqNum:   tc.seqNum,
				Payload:  []byte("test"),
			}

			encoded := protocol.Encode(original)
			decoded, err := protocol.Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			if decoded.SocketID != tc.socketID {
				t.Errorf("SocketID mismatch: got 0x%08X, want 0x%08X", decoded.SocketID, tc.socketID)
			}
			if decoded.SeqNum != tc.seqNum {
				t.Errorf("SeqNum mismatch: got %d, want %d", decoded.SeqNum, tc.seqNum)
			}
		})
	}
}

// TestEncodeAllPacketTypes ensures all three packet types can be encoded
// and decoded correctly.
func TestEncodeAllPacketTypes(t *testing.T) {
	types := []struct {
		name     string
		typeCode uint8
	}{
		{"TypeConnect", protocol.TypeConnect},
		{"TypeData", protocol.TypeData},
		{"TypeClose", protocol.TypeClose},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			pkt := &protocol.Packet{
				Type:     tt.typeCode,
				SocketID: 0x11111111,
				SeqNum:   222,
				Payload:  []byte("payload"),
			}

			encoded := protocol.Encode(pkt)
			decoded, err := protocol.Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			if decoded.Type != tt.typeCode {
				t.Errorf("Type mismatch: got %d, want %d", decoded.Type, tt.typeCode)
			}
		})
	}
}

// TestEncodeLargePayload verifies that large payloads are handled correctly.
func TestEncodeLargePayload(t *testing.T) {
	sizes := []int{
		1024,       // 1 KB
		16 * 1024,  // 16 KB
		64 * 1024,  // 64 KB
		256 * 1024, // 256 KB
	}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i % 256)
			}

			pkt := &protocol.Packet{
				Type:     protocol.TypeData,
				SocketID: 0x99999999,
				SeqNum:   1,
				Payload:  payload,
			}

			encoded := protocol.Encode(pkt)
			decoded, err := protocol.Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed for size %d: %v", size, err)
			}

			if !bytes.Equal(decoded.Payload, payload) {
				t.Errorf("Payload mismatch for size %d", size)
			}
		})
	}
}

// TestDecodePreservesPayload verifies that the payload is correctly copied
// and not aliased to the input buffer.
func TestDecodePreservesPayload(t *testing.T) {
	original := &protocol.Packet{
		Type:     protocol.TypeData,
		SocketID: 0x12345678,
		SeqNum:   10,
		Payload:  []byte("original"),
	}

	encoded := protocol.Encode(original)
	decoded, err := protocol.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	// Modify the original encoded buffer
	if len(encoded) > protocol.HeaderSize {
		encoded[protocol.HeaderSize] = 0xFF
	}

	// Verify decoded payload is unchanged
	if !bytes.Equal(decoded.Payload, []byte("original")) {
		t.Errorf("Payload was incorrectly aliased: got %v", decoded.Payload)
	}
}
