// Package logger provides structured logging for NanoClaw.
package logger

import (
	"os"
	"time"

	"github.com/rs/zerolog"
)

// Logger is the global logger instance.
var Logger zerolog.Logger

func init() {
	// Configure zerolog
	zerolog.TimeFieldFormat = time.RFC3339

	// Get log level from environment
	level := zerolog.InfoLevel
	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		if parsed, err := zerolog.ParseLevel(envLevel); err == nil {
			level = parsed
		}
	}

	// Create console writer with colors
	output := zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: "15:04:05",
	}

	Logger = zerolog.New(output).
		Level(level).
		With().
		Timestamp().
		Logger()
}

// Debug logs a debug message.
func Debug() *zerolog.Event {
	return Logger.Debug()
}

// Info logs an info message.
func Info() *zerolog.Event {
	return Logger.Info()
}

// Warn logs a warning message.
func Warn() *zerolog.Event {
	return Logger.Warn()
}

// Error logs an error message.
func Error() *zerolog.Event {
	return Logger.Error()
}

// Fatal logs a fatal message and exits.
func Fatal() *zerolog.Event {
	return Logger.Fatal()
}

// WithComponent returns a logger with a component field.
func WithComponent(component string) zerolog.Logger {
	return Logger.With().Str("component", component).Logger()
}
