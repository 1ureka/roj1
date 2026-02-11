// Package config holds the CLI configuration types.
package config

// Role represents the user's chosen role (host or client).
type Role string

const (
	RoleHost   Role = "host"
	RoleClient Role = "client"
)

// Config stores all parameters gathered from the interactive CLI prompts.
type Config struct {
	Role       Role
	TargetPort int    // Host: the TCP service port to forward
	LocalPort  int    // Client: local port for the virtual service
	WSURL      string // Client: WebSocket URL to connect to
}
