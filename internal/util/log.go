package util

import (
	"fmt"

	"github.com/pterm/pterm"
)

func init() {
	pterm.DefaultLogger.ShowTime = true
	pterm.DefaultLogger.TimeFormat = "02 Jan 15:04:05"
	pterm.DefaultLogger.MaxWidth = 1000
}

// Leveled logging functions backed by pterm prefixed printers.
// All output goes to stderr by default (pterm's default).

func LogDebug(format string, args ...interface{}) {
	pterm.DefaultLogger.Debug(fmt.Sprintf(format, args...))
}

func LogInfo(format string, args ...interface{}) {
	pterm.DefaultLogger.Info(fmt.Sprintf(format, args...))
}

func LogSuccess(format string, args ...interface{}) {
	pterm.DefaultLogger.Info(fmt.Sprintf(format, args...))
}

func LogWarning(format string, args ...interface{}) {
	pterm.DefaultLogger.Warn(fmt.Sprintf(format, args...))
}

func LogError(format string, args ...interface{}) {
	pterm.DefaultLogger.Error(fmt.Sprintf(format, args...))
}

// EnableDebug configures the logger to show debug messages.
func EnableDebug() {
	pterm.DefaultLogger.Level = pterm.LogLevelDebug
}
