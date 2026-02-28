// Roj1 — CLI entry point.
//
// This tool creates a P2P tunnel over WebRTC DataChannel, forwarding a remote
// TCP service to a local port. No relay servers are needed after the signaling
// phase (which uses WebSocket).
//
// It can be launched interactively (no flags) or non-interactively via CLI
// flags (-role, -port, -wsPort, -wsUrl, -wsListen).
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

	"github.com/1ureka/roj1/internal/adapter"
	"github.com/1ureka/roj1/internal/signaling"
	"github.com/1ureka/roj1/internal/util"
)

var version = "dev"

func main() {
	// Root context — cancelled on Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// CLI flags.
	role := flag.String("role", "", "Role: host or client")
	port := flag.Int("port", 0, "Target port (host) or virtual service port (client), 1~65535")
	wsPortFlag := flag.Int("wsPort", 0, "WebSocket signaling server port (host only)")
	wsURLFlag := flag.String("wsUrl", "", "WebSocket URL to connect to (client only)")
	wsListenFlag := flag.Bool("wsListen", false, "Listen on all network interfaces (host only, for LAN access)")
	debugMode := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	if *debugMode {
		util.EnableDebug()
	}

	pterm.Info.Println(fmt.Sprintf("Roj1 — v%s", version))
	pterm.Println()

	switch *role {
	case "":
		// No -role flag → interactive mode.
		runInteractive(ctx)

	case "host":
		if *port < 1 || *port > 65535 {
			util.LogError("invalid or missing -port (must be 1~65535)")
			os.Exit(1)
		}

		var wsAddr string

		switch {
		case *wsListenFlag:
			wsAddr = fmt.Sprintf(":%d", *wsPortFlag)
		case *wsPortFlag > 0:
			wsAddr = fmt.Sprintf("127.0.0.1:%d", *wsPortFlag)
		default:
			wsAddr = ":0"
		}

		runHost(ctx, *port, wsAddr)

	case "client":
		if *port < 1 || *port > 65535 {
			util.LogError("invalid or missing -port (must be 1~65535)")
			os.Exit(1)
		}

		if *wsURLFlag == "" {
			util.LogError("missing -wsUrl for client role")
			os.Exit(1)
		}

		wsURL, err := normalizeWSURL(*wsURLFlag)

		if err != nil {
			util.LogError("%v", err)
			os.Exit(1)
		}

		runClient(ctx, *port, wsURL)

	default:
		util.LogError("invalid -role: must be 'host' or 'client'")
		os.Exit(1)
	}

	util.LogInfo("successfully closed tunnel connection")
}

// ---------------------------------------------------------------------------
// Run modes
// ---------------------------------------------------------------------------

// runInteractive falls back to the original interactive prompts when no -role
// flag is provided.
func runInteractive(ctx context.Context) {
	role, _ := pterm.DefaultInteractiveSelect.
		WithOptions([]string{"Host  — Expose a local service", "Client — Connect to a remote host"}).
		WithDefaultText("Select your role").
		Show()

	pterm.Println()

	if strings.HasPrefix(role, "Host") {
		port := askPort("Target port to forward (1 ~ 65535)")
		runHost(ctx, port, ":0")
	} else {
		wsURL := askURL()
		port := askPort("Local port for virtual service (1 ~ 65535)")
		runClient(ctx, port, wsURL)
	}
}

// runHost executes the host-side tunnel logic.
func runHost(ctx context.Context, port int, wsAddr string) {
	tr, err := signaling.EstablishAsHost(ctx, wsAddr)
	if err != nil {
		util.LogError("failed to establish tunnel: %v", err)
		os.Exit(1)
	}
	defer tr.Close()

	util.StartStatsReporter(ctx)
	util.LogSuccess("P2P tunnel established — forwarding traffic to 127.0.0.1:%d", port)

	if err := adapter.RunAsHost(ctx, tr, fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		util.LogError("failed to handle tunnel connection: %v", err)
		os.Exit(1)
	}
}

// runClient executes the client-side tunnel logic.
func runClient(ctx context.Context, port int, wsURL string) {
	tr, err := signaling.EstablishAsClient(ctx, wsURL)
	if err != nil {
		util.LogError("failed to establish tunnel: %v", err)
		os.Exit(1)
	}
	defer tr.Close()

	util.StartStatsReporter(ctx)
	util.LogSuccess("P2P tunnel established — forwarding traffic to Host")

	if err := adapter.RunAsClient(ctx, tr, fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		util.LogError("failed to handle tunnel connection: %v", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// Helper Functions
// ---------------------------------------------------------------------------

// normalizeWSURL validates and normalizes a raw WebSocket URL string.
func normalizeWSURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid WebSocket URL: %s", raw)
	}
	scheme := "wss"
	if u.Scheme == "ws" || u.Scheme == "wss" {
		scheme = u.Scheme
	}
	return fmt.Sprintf("%s://%s/ws", scheme, u.Host), nil
}

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

		util.LogWarning("invalid port number: must be 1 ~ 65535")
		pterm.Println()
	}
}

// askURL prompts the user for a valid WebSocket URL until one is entered.
func askURL() string {
	for {
		raw, _ := pterm.DefaultInteractiveTextInput.
			WithDefaultText("WebSocket URL (e.g. wss://***.asse.devtunnels.ms/ws)").
			Show()

		wsURL, err := normalizeWSURL(raw)
		if err == nil {
			pterm.Println()
			return wsURL
		}

		pterm.Println()
		util.LogWarning("invalid input: please enter a valid host or URL")
	}
}
