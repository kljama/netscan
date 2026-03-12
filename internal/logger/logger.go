package logger

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Setup initializes the global logger with appropriate settings
// - debugMode param still works, but we also honor DEBUG=true env var (case-insensitive)
// - if ENVIRONMENT=development we use a human-friendly console writer
// - Caller() is enabled so debug lines include file:line (helps track false positives)
func Setup(debugMode bool) {
	// Human-friendly console output for local development
	if os.Getenv("ENVIRONMENT") == "development" {
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		})
	}

	// allow enabling debug via env var DEBUG=true as a quick toggle
	if debugMode || strings.EqualFold(os.Getenv("DEBUG"), "true") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Add common fields and caller information to help trace where logs originate
	log.Logger = log.With().
		Str("service", "netscan").
		Timestamp().
		Caller().
		Logger()
}

// Get returns a logger with context
func Get() zerolog.Logger {
	return log.Logger
}

// With returns a logger with additional context
func With(key string, value interface{}) zerolog.Logger {
	return log.With().Interface(key, value).Logger()
}
