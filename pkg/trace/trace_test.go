package trace

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
)

func TestNewTracer(t *testing.T) {
	// Test with normal log level
	tracer := NewTracer("TEST", LogLevelNormal)
	if tracer.prefix != "TEST" {
		t.Errorf("Expected prefix 'TEST', got '%s'", tracer.prefix)
	}
	if tracer.level != LogLevelNormal {
		t.Errorf("Expected level LogLevelNormal, got %v", tracer.level)
	}
	if tracer.verbose {
		t.Errorf("Expected verbose=false, got true")
	}

	// Test with verbose log level
	tracer = NewTracer("DEBUG", LogLevelVerbose)
	if tracer.prefix != "DEBUG" {
		t.Errorf("Expected prefix 'DEBUG', got '%s'", tracer.prefix)
	}
	if tracer.level != LogLevelVerbose {
		t.Errorf("Expected level LogLevelVerbose, got %v", tracer.level)
	}
	if !tracer.verbose {
		t.Errorf("Expected verbose=true, got false")
	}
}

func TestWithContext(t *testing.T) {
	ctx := context.Background()
	tracer := NewTracer("TEST", LogLevelNormal)

	// Add tracer to context
	tracedCtx := WithContext(ctx, tracer)

	// Extract tracer from context
	extracted := tracedCtx.Value(traceKey).(*Tracer)
	if extracted != tracer {
		t.Errorf("Expected to extract the same tracer that was put in context")
	}
}

func TestFromContext(t *testing.T) {
	// Test with a tracer in the context
	ctx := context.Background()
	tracer := NewTracer("TEST", LogLevelNormal)
	tracedCtx := WithContext(ctx, tracer)

	extracted := FromContext(tracedCtx)
	if extracted != tracer {
		t.Errorf("Expected FromContext to return the tracer we put in")
	}

	// Test with no tracer in the context
	emptyCtx := context.Background()
	defaultTracer := FromContext(emptyCtx)

	if defaultTracer == nil {
		t.Errorf("Expected a default tracer, got nil")
	} else {
		if defaultTracer.prefix != "" {
			t.Errorf("Expected empty prefix for default tracer, got '%s'", defaultTracer.prefix)
		}
		if defaultTracer.level != LogLevelNormal {
			t.Errorf("Expected level LogLevelNormal for default tracer, got %v", defaultTracer.level)
		}
	}
}

func TestSetVerbose(t *testing.T) {
	tracer := NewTracer("TEST", LogLevelNormal)
	if tracer.verbose {
		t.Errorf("Expected initial verbose=false, got true")
	}

	// Set to verbose
	tracer.SetVerbose(true)
	if !tracer.verbose {
		t.Errorf("Expected verbose=true after SetVerbose(true), got false")
	}
	if tracer.level != LogLevelVerbose {
		t.Errorf("Expected level LogLevelVerbose after SetVerbose(true), got %v", tracer.level)
	}

	// Set back to normal
	tracer.SetVerbose(false)
	if tracer.verbose {
		t.Errorf("Expected verbose=false after SetVerbose(false), got true")
	}
	if tracer.level != LogLevelNormal {
		t.Errorf("Expected level LogLevelNormal after SetVerbose(false), got %v", tracer.level)
	}
}

func TestIsVerbose(t *testing.T) {
	tracer := NewTracer("TEST", LogLevelNormal)
	if tracer.IsVerbose() {
		t.Errorf("Expected IsVerbose()=false for normal tracer, got true")
	}

	tracer = NewTracer("TEST", LogLevelVerbose)
	if !tracer.IsVerbose() {
		t.Errorf("Expected IsVerbose()=true for verbose tracer, got false")
	}
}

func TestInfof(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	tracer := NewTracer("TEST", LogLevelNormal)
	tracer.Infof("Test message %d", 123)

	output := buf.String()
	if !strings.Contains(output, "TEST: Test message 123") {
		t.Errorf("Expected log output to contain 'TEST: Test message 123', got '%s'", output)
	}

	// Test without prefix
	buf.Reset()
	tracer = NewTracer("", LogLevelNormal)
	tracer.Infof("Plain message %d", 456)

	output = buf.String()
	if !strings.Contains(output, "Plain message 456") {
		t.Errorf("Expected log output to contain 'Plain message 456', got '%s'", output)
	}
	if strings.Contains(output, ": Plain message") {
		t.Errorf("Expected no prefix in log output, got '%s'", output)
	}
}

func TestDebugf(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	// Test with normal log level (debug messages should be suppressed)
	tracer := NewTracer("TEST", LogLevelNormal)
	tracer.Debugf("Debug message %d", 123)

	output := buf.String()
	if output != "" {
		t.Errorf("Expected no debug output with normal log level, got '%s'", output)
	}

	// Test with verbose log level
	buf.Reset()
	tracer = NewTracer("TEST", LogLevelVerbose)
	tracer.Debugf("Debug message %d", 456)

	output = buf.String()
	if !strings.Contains(output, "TEST: Debug message 456") {
		t.Errorf("Expected log output to contain 'TEST: Debug message 456', got '%s'", output)
	}
}

func TestError(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	// Test with prefix
	tracer := NewTracer("TEST", LogLevelNormal)
	err := errors.New("test error")
	tracer.Error(err)

	output := buf.String()
	if !strings.Contains(output, "TEST ERROR: test error") {
		t.Errorf("Expected log output to contain 'TEST ERROR: test error', got '%s'", output)
	}

	// Test without prefix
	buf.Reset()
	tracer = NewTracer("", LogLevelNormal)
	tracer.Error(err)

	output = buf.String()
	if !strings.Contains(output, "ERROR: test error") {
		t.Errorf("Expected log output to contain 'ERROR: test error', got '%s'", output)
	}
}

func TestWithPrefix(t *testing.T) {
	original := NewTracer("ORIG", LogLevelVerbose)

	// Create a new tracer with a different prefix
	child := original.WithPrefix("CHILD")

	if child.prefix != "CHILD" {
		t.Errorf("Expected prefix 'CHILD', got '%s'", child.prefix)
	}

	// The child should inherit the verbosity setting
	if child.level != LogLevelVerbose {
		t.Errorf("Expected child to inherit LogLevelVerbose, got %v", child.level)
	}
	if !child.verbose {
		t.Errorf("Expected child to inherit verbose=true, got false")
	}

	// The original should be unchanged
	if original.prefix != "ORIG" {
		t.Errorf("Expected original prefix to remain 'ORIG', got '%s'", original.prefix)
	}
}
