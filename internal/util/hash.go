// Package util provides shared utility functions.
package util

import (
	"hash/fnv"
	"net"
)

// SocketIDFromConn computes a 4-byte hash from a TCP connection's 4-tuple
// (local IP, local port, remote IP, remote port). The hash is used solely
// for identification and does not need to be reversible.
func SocketIDFromConn(conn net.Conn) uint32 {
	h := fnv.New32a()
	h.Write([]byte(conn.LocalAddr().String()))
	h.Write([]byte(conn.RemoteAddr().String()))
	return h.Sum32()
}
