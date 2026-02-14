package util

import "github.com/pterm/pterm"

func init() {
	pterm.DefaultLogger.ShowTime = true
	pterm.DefaultLogger.TimeFormat = "02 Jan 2006 15:04:05"
}

// Leveled logging functions backed by pterm prefixed printers.
// All output goes to stderr by default (pterm's default).

func LogDebug(format string, args ...interface{}) {
	pterm.Debug.Printfln(format, args...)
}

func LogInfo(format string, args ...interface{}) {
	pterm.Info.Printfln(format, args...)
}

func LogSuccess(format string, args ...interface{}) {
	pterm.Success.Printfln(format, args...)
}

func LogWarning(format string, args ...interface{}) {
	pterm.Warning.Printfln(format, args...)
}

func LogError(format string, args ...interface{}) {
	pterm.Error.Printfln(format, args...)
}

// EnableDebug configures the logger to show debug messages.
func EnableDebug() {
	pterm.DefaultLogger.Level = pterm.LogLevelDebug
}
