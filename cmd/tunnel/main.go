// P2P TCP Tunnel — CLI entry point.
//
// This tool creates a P2P tunnel over WebRTC DataChannel, forwarding a remote
// TCP service to a local port. No relay servers are needed after the signaling
// phase (which uses WebSocket).
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/pterm/pterm"

	"github.com/1ureka/1ureka.net.p2p/internal/adapter"
	"github.com/1ureka/1ureka.net.p2p/internal/signaling"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

func main() {
	// Root context — cancelled on Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	debugMode := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	if *debugMode {
		util.EnableDebug()
	}

	util.LogInfo("Welcome to 1ureka.net.p2p CLI!")
	pterm.Println()

	role, _ := pterm.DefaultInteractiveSelect.
		WithOptions([]string{"Host  — Expose a local service", "Client — Connect to a remote host"}).
		WithDefaultText("Select your role").
		Show()

	pterm.Println()

	// ---- Run the appropriate tunnel logic based on the selected role ------------------------------

	if strings.HasPrefix(role, "Host") {
		port := askPort("Target port to forward (1 ~ 65535)")

		tr, err := signaling.EstablishAsHost(ctx)
		if err != nil {
			util.LogError("Failed to establish tunnel: %v", err)
			os.Exit(1)
		}
		defer tr.Close()

		util.LogSuccess("P2P tunnel established — forwarding traffic to 127.0.0.1:%d", port)

		if err := adapter.RunAsHost(ctx, tr, fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			util.LogError("Tunnel error: %v", err)
			os.Exit(1)
		}
	} else {
		wsURL := askURL()
		port := askPort("Local port for virtual service (1 ~ 65535)")

		tr, err := signaling.EstablishAsClient(ctx, wsURL)
		if err != nil {
			util.LogError("Failed to establish tunnel: %v", err)
			os.Exit(1)
		}
		defer tr.Close()

		util.LogSuccess("P2P tunnel established — forwarding traffic to Host")

		if err := adapter.RunAsClient(ctx, tr, fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			util.LogError("Tunnel error: %v", err)
			os.Exit(1)
		}
	}

	util.LogInfo("Tunnel closed")
}

// ----------- Helper Functions ------------------------------------------------------

// askPort prompts the user for a port number until a valid one is entered.
func askPort(prompt string) int {
	for {
		raw, _ := pterm.DefaultInteractiveTextInput.
			WithDefaultText(prompt).
			Show()

		port, err := strconv.Atoi(strings.TrimSpace(raw))
		if err == nil && port >= 1 && port <= 65535 {
			pterm.Println()
			return port
		}

		util.LogWarning("Invalid port number: must be 1 ~ 65535")
		pterm.Println()
	}
}

// askURL prompts the user for a WebSocket URL until a non-empty one is entered.
func askURL() string {
	for {
		raw, _ := pterm.DefaultInteractiveTextInput.
			WithDefaultText("WebSocket URL (e.g. wss://***.asse.devtunnels.ms/ws)").
			Show()

		url, err := url.Parse(strings.TrimSpace(raw))
		if err == nil && (url.Scheme == "ws" || url.Scheme == "wss") {
			if url.Path == "/ws" {
				pterm.Println()
				return url.String()
			}
		}

		pterm.Println()
		util.LogWarning("Invalid URL: must start with ws:// or wss:// and cannot be empty")
	}
}
