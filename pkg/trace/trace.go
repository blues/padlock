package trace

import (
	"context"
	"fmt"
	"log"
	"os"
)

// LogLevel represents tracing verbosity level
type LogLevel int

const (
	// LogLevelNormal for regular user-facing messages
	LogLevelNormal LogLevel = iota
	// LogLevelVerbose for detailed debug/trace info (includes all trace information)
	LogLevelVerbose
)

// TracerKey is the key type used for storing tracers in context
type TracerKey struct{}

// Alternative key type for context value access
type traceKeyType string

const traceKey traceKeyType = "tracer"

// Tracer provides a context-aware tracing interface
type Tracer struct {
	prefix  string
	level   LogLevel
	verbose bool
}

// NewTracer creates a new tracer instance
func NewTracer(prefix string, level LogLevel) *Tracer {
	return &Tracer{
		prefix:  prefix,
		level:   level,
		verbose: level >= LogLevelVerbose,
	}
}

// Tracef logs a message at the TRACE level (included in verbose output)
func (t *Tracer) Tracef(format string, args ...interface{}) {
	if !t.verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s TRACE: %s", t.prefix, msg)
}

// WithContext adds the tracer to the given context
func WithContext(ctx context.Context, tracer *Tracer) context.Context {
	// Store with both key types for consistent access patterns
	ctx = context.WithValue(ctx, TracerKey{}, tracer)
	return context.WithValue(ctx, traceKey, tracer)
}

// FromContext extracts the tracer from the context
func FromContext(ctx context.Context) *Tracer {
	// Check for tracer using struct key
	if tracer, ok := ctx.Value(TracerKey{}).(*Tracer); ok {
		return tracer
	}
	// Try alternative key format
	if tracer, ok := ctx.Value(traceKey).(*Tracer); ok {
		return tracer
	}
	// Return a default tracer if none found in context
	return NewTracer("", LogLevelNormal)
}

// SetVerbose updates the verbose flag
func (t *Tracer) SetVerbose(verbose bool) {
	t.verbose = verbose
	if verbose {
		t.level = LogLevelVerbose
	} else {
		t.level = LogLevelNormal
	}
}

// IsVerbose returns whether verbose tracing is enabled
func (t *Tracer) IsVerbose() bool {
	return t.verbose
}

// Infof logs a formatted message at normal level
func (t *Tracer) Infof(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if t.prefix != "" {
		log.Printf("%s: %s", t.prefix, msg)
	} else {
		log.Print(msg)
	}
}

// Debugf logs a formatted message only if verbose is enabled
func (t *Tracer) Debugf(format string, args ...interface{}) {
	if !t.verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s: %s", t.prefix, msg)
}

// Error logs an error message
func (t *Tracer) Error(err error) {
	if t.prefix != "" {
		log.Printf("%s ERROR: %v", t.prefix, err)
	} else {
		log.Printf("ERROR: %v", err)
	}
}

// Fatal logs a fatal error and exits
func (t *Tracer) Fatal(err error) {
	if t.prefix != "" {
		log.Fatalf("%s FATAL: %v", t.prefix, err)
	} else {
		log.Fatalf("FATAL: %v", err)
	}
	os.Exit(1)
}

// WithPrefix creates a new tracer with the given prefix
func (t *Tracer) WithPrefix(prefix string) *Tracer {
	return &Tracer{
		prefix:  prefix,
		level:   t.level,
		verbose: t.verbose,
	}
}

// GetPrefix returns the tracer's prefix
func (t *Tracer) GetPrefix() string {
	return t.prefix
}
