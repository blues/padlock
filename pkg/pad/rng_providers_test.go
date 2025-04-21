// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

package pad

import (
	"context"
	"testing"

	"github.com/blues/padlock/pkg/trace"
)

// TestCryptoRandRandomness tests the randomness of CryptoRand
func TestCryptoRandRandomness(t *testing.T) {
	// Create a context with tracing
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a CryptoRand instance
	rng := &CryptoRand{}

	// Test buffer (larger sample for statistical tests)
	const bufSize = 100000
	buf := make([]byte, bufSize)

	// Get random bytes
	err := rng.Read(ctx, buf)
	if err != nil {
		t.Fatalf("CryptoRand read failed: %v", err)
	}

	// Run statistical tests on the output
	runRandomnessTests(t, "CryptoRand", buf)
}

// TestMathRandRandomness tests the randomness of MathRand
func TestMathRandRandomness(t *testing.T) {
	// Create a context with tracing
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a MathRand instance
	rng := NewMathRand()

	// Test buffer (larger sample for statistical tests)
	const bufSize = 100000
	buf := make([]byte, bufSize)

	// Get random bytes
	err := rng.Read(ctx, buf)
	if err != nil {
		t.Fatalf("MathRand read failed: %v", err)
	}

	// Run statistical tests on the output
	runRandomnessTests(t, "MathRand", buf)
}

// TestChaCha20RandRandomness tests the randomness of ChaCha20Rand
func TestChaCha20RandRandomness(t *testing.T) {
	// Create a context with tracing
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a ChaCha20Rand instance
	rng := NewChaCha20Rand()

	// Test buffer (larger sample for statistical tests)
	const bufSize = 100000
	buf := make([]byte, bufSize)

	// Get random bytes
	err := rng.Read(ctx, buf)
	if err != nil {
		t.Fatalf("ChaCha20Rand read failed: %v", err)
	}

	// Run statistical tests on the output
	runRandomnessTests(t, "ChaCha20Rand", buf)
}

// TestPCG64RandRandomness tests the randomness of PCG64Rand (math/rand/v2 implementation)
func TestPCG64RandRandomness(t *testing.T) {
	// Create a context with tracing
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a PCG64Rand instance
	rng := NewPCG64Rand()

	// Test buffer (larger sample for statistical tests)
	const bufSize = 100000
	buf := make([]byte, bufSize)

	// Get random bytes
	err := rng.Read(ctx, buf)
	if err != nil {
		t.Fatalf("PCG64Rand read failed: %v", err)
	}

	// Run statistical tests on the output
	runRandomnessTests(t, "PCG64Rand", buf)
}

// TestMT19937RandRandomness tests the randomness of MT19937Rand
func TestMT19937RandRandomness(t *testing.T) {
	// Create a context with tracing
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a MT19937Rand instance
	rng := NewMT19937Rand()

	// Test buffer (larger sample for statistical tests)
	const bufSize = 100000
	buf := make([]byte, bufSize)

	// Get random bytes
	err := rng.Read(ctx, buf)
	if err != nil {
		t.Fatalf("MT19937Rand read failed: %v", err)
	}

	// Run statistical tests on the output
	runRandomnessTests(t, "MT19937Rand", buf)
}

// TestTestRNGPredictability verifies that TestRNG produces predictable sequences
func TestTestRNGPredictability(t *testing.T) {
	// Create a context with tracing
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create two TestRNG instances
	rng1 := &TestRNG{}
	rng2 := &TestRNG{}

	// Test buffer
	buf1 := make([]byte, 1024)
	buf2 := make([]byte, 1024)

	// Get random bytes from both RNGs
	err := rng1.Read(ctx, buf1)
	if err != nil {
		t.Fatalf("TestRNG read failed: %v", err)
	}

	err = rng2.Read(ctx, buf2)
	if err != nil {
		t.Fatalf("TestRNG read failed: %v", err)
	}

	// Verify that both RNGs produced the same sequence
	for i := 0; i < len(buf1); i++ {
		if buf1[i] != buf2[i] {
			t.Errorf("TestRNG instances produced different sequences at index %d: %d != %d", i, buf1[i], buf2[i])
			break
		}
	}

	// Verify the sequence matches expectations (counter should increment by 1 each time)
	for i := 0; i < len(buf1); i++ {
		if buf1[i] != byte(i) {
			t.Errorf("TestRNG did not produce expected sequence at index %d: got %d, want %d", i, buf1[i], i)
			break
		}
	}
}
