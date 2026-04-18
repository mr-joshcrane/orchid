package minilog

import (
	"io"
	"strings"
)

// Level defines the severity level of a log message.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

// Logger holds the state for logging operations.
type Logger struct {
	w          io.Writer
	minLevel   Level
	fields     map[string]string
	formatter  func(Level, string, map[string]string) string
}

// New creates a new Logger instance writing to the provided io.Writer.
func New(w io.Writer) *Logger {
	return &Logger{w: w, minLevel: LevelInfo, fields: make(map[string]string), formatter: nil}
}

// WithFields returns a new Logger instance with the provided fields merged into the context.
func (l *Logger) WithFields(fields map[string]string) *Logger {
	newLogger := *l
	newLogger.fields = fields
	return &newLogger
}

// SetLevel sets the minimum logging level required for messages to be processed.
func (l *Logger) SetLevel(level Level) {
	l.minLevel = level
}

// SetFormatter sets a custom function to format log messages.
// The function receives the level, message, and current fields, and must return the formatted string.
func (l *Logger) SetFormatter(fn func(Level, string, map[string]string) string) {
	l.formatter = fn
}

// log handles the core logic of logging, checking levels, formatting, and writing to the writer.
func (l *Logger) log(level Level, msg string, kvs []string) {
	if level < l.minLevel {
		return
	}

	// 1. Format the base message using the formatter if available
	formattedMsg := ""
	if l.formatter != nil {
		formattedMsg = l.formatter(level, msg, l.fields)
	} else {
		formattedMsg = msg
	}

	// 2. Append KVs if provided
	if len(kvs) > 0 {
		formattedMsg += " " + strings.Join(kvs, " ")
	}

	// 3. Write to writer
	_, _ = l.w.Write([]byte(formattedMsg + "\n"))
}

// Info logs an informational message.
func (l *Logger) Info(msg string, kvs ...string) {
	if l.minLevel > LevelInfo {
		return
	}
	l.log(LevelInfo, msg, kvs)
}

// Warn logs a warning message.
func (l *Logger) Warn(msg string, kvs ...string) {
	if l.minLevel > LevelWarn {
		return
	}
	l.log(LevelWarn, msg, kvs)
}

// Error logs an error message.
func (l *Logger) Error(msg string, kvs ...string) {
	if l.minLevel > LevelError {
		return
	}
	// Reusing the core log function for consistency
	l.log(LevelError, msg, kvs)
}