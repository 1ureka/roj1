package util

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/pterm/pterm"
)

// ──────────────────────────────────────────────────────────────────────────────
// Global stats singleton
// ──────────────────────────────────────────────────────────────────────────────

// Stats is the process-wide traffic/connection counter.
var Stats = &stats{}

type stats struct {
	TotalConns  atomic.Int64 // cumulative count of connections since process start
	ClosedConns atomic.Int64 // cumulative count of closed connections since process start
	BytesSent   atomic.Int64 // cumulative bytes written to DataChannel
	BytesRecv   atomic.Int64 // cumulative bytes read  from DataChannel
}

func (s *stats) AddConn()      { s.TotalConns.Add(1) }
func (s *stats) RemoveConn()   { s.ClosedConns.Add(1) }
func (s *stats) AddSent(n int) { s.BytesSent.Add(int64(n)) }
func (s *stats) AddRecv(n int) { s.BytesRecv.Add(int64(n)) }

// ──────────────────────────────────────────────────────────────────────────────
// Periodic reporter
// ──────────────────────────────────────────────────────────────────────────────

// StartStatsReporter launches a goroutine that logs tunnel statistics
// every 10 seconds. It stops when ctx is cancelled.
func StartStatsReporter(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		var prevSent, prevRecv, prevTotal, prevClosed int64
		for {
			select {
			case <-ticker.C:
				total := Stats.TotalConns.Load()
				closed := Stats.ClosedConns.Load()
				sent := Stats.BytesSent.Load()
				recv := Stats.BytesRecv.Load()

				inS := float64(sent-prevSent) / 10.0
				outS := float64(recv-prevRecv) / 10.0
				inC := total - prevTotal
				outC := closed - prevClosed

				if inC > 0 || outC > 0 || inS > 10 || outS > 10 {
					pterm.DefaultLogger.Info(formatStats(inS, outS, inC, outC))
				}

				prevSent = sent
				prevRecv = recv
				prevTotal = total
				prevClosed = closed

			case <-ctx.Done():
				return
			}
		}
	}()
}

// byteUnits defines the units for formatting byte counts in a human-readable way.
var byteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

// formatBytes formats a byte count into a human-readable string with fixed width (exactly 8 chars)
// for example: "99.0   B", " 1.5 KiB", " 0.1 MiB", "98.9 GiB", etc.
func formatBytes(b float64) string {
	unitIdx := 0

	// to prevent "100.0 KiB", which is 9 chars
	for b > 99 && unitIdx < 5 {
		b /= 1024
		unitIdx++
	}

	return fmt.Sprintf("%4.1f %3s", b, byteUnits[unitIdx])
}

// formatStats returns a formatted string of the current stats for display in the logger.
func formatStats(inS, outS float64, inC, outC int64) string {
	return fmt.Sprintf("In: %s/s | Out: %s/s | Conn: %2d↑ %2d↓",
		formatBytes(inS),
		formatBytes(outS),
		inC,
		outC,
	)
}
