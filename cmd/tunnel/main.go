// P2P TCP Tunnel — CLI entry point.
//
// This tool creates a P2P tunnel over WebRTC DataChannel, forwarding a remote
// TCP service to a local port. No relay servers are needed after the signaling
// phase (which uses WebSocket).
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/1ureka/1ureka.net.p2p/internal/adapter"
	"github.com/1ureka/1ureka.net.p2p/internal/signaling"
)

func main() {
	// Root context — cancelled on Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("╔══════════════════════════════════════╗")
	fmt.Println("║       P2P TCP Tunnel (WebRTC)        ║")
	fmt.Println("╚══════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("請選擇角色：")
	fmt.Println("  1) Host  （提供服務的一方）")
	fmt.Println("  2) Client（存取服務的一方）")
	fmt.Print("\n請輸入選擇 (1/2): ")

	scanner.Scan()
	choice := strings.TrimSpace(scanner.Text())

	switch choice {
	case "1":
		runHost(ctx, scanner)
	case "2":
		runClient(ctx, scanner)
	default:
		fmt.Println("無效選擇，請輸入 1 或 2")
		os.Exit(1)
	}
}

func runHost(ctx context.Context, scanner *bufio.Scanner) {
	fmt.Print("請輸入要轉發的目標 port: ")
	scanner.Scan()
	port, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || port < 1 || port > 65535 {
		fmt.Println("無效的 port 號碼")
		os.Exit(1)
	}

	tr, err := signaling.EstablishAsHost(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "錯誤: %v\n", err)
		os.Exit(1)
	}
	defer tr.Close()

	fmt.Println("✓ P2P 隧道已建立！正在轉發流量...")

	targetAddr := fmt.Sprintf("127.0.0.1:%d", port)
	if err := adapter.RunAsHost(ctx, tr, targetAddr); err != nil {
		fmt.Fprintf(os.Stderr, "錯誤: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("隧道已關閉")
}

func runClient(ctx context.Context, scanner *bufio.Scanner) {
	fmt.Print("請輸入 WebSocket URL (例如 wss://***.asse.devtunnels.ms/ws): ")
	scanner.Scan()
	wsURL := strings.TrimSpace(scanner.Text())
	if wsURL == "" {
		fmt.Println("URL 不可為空")
		os.Exit(1)
	}

	fmt.Print("請輸入本地監聽 port: ")
	scanner.Scan()
	port, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || port < 1 || port > 65535 {
		fmt.Println("無效的 port 號碼")
		os.Exit(1)
	}

	tr, err := signaling.EstablishAsClient(ctx, wsURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "錯誤: %v\n", err)
		os.Exit(1)
	}
	defer tr.Close()

	fmt.Println("✓ P2P 隧道已建立！")
	fmt.Printf("正在監聽 127.0.0.1:%d，流量將透過 P2P 隧道轉發至 Host\n", port)

	if err := adapter.RunAsClient(ctx, tr, port); err != nil {
		fmt.Fprintf(os.Stderr, "錯誤: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("隧道已關閉")
}
