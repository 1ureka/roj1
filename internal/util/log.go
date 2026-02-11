package util

import (
	"log"
	"os"
)

// Logger is the shared logger instance. Output goes to stderr so that
// interactive prompts on stdout are not interleaved with log messages.
var Logger = log.New(os.Stderr, "", log.LstdFlags)

// Logf writes a formatted log message.
func Logf(format string, args ...interface{}) {
	Logger.Printf(format, args...)
}
