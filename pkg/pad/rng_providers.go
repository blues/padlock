// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

// This file contains implementations of various random number generator providers
// used by the padlock system.

package pad

import (
	"context"
	"crypto/cipher"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	rand2 "math/rand/v2"
	"sync"
	"time"

	"github.com/blues/padlock/pkg/trace"
	"github.com/seehuhn/mt19937"
	"golang.org/x/crypto/chacha20"
)

// CryptoRand is the primary source of randomness for the padlock system.
//
// This implementation uses Go's crypto/rand package, which interfaces with the
// operating system's cryptographically secure random number generator (CSRNG).
// On Unix-like systems, this typically means /dev/urandom or /dev/random,
// while on Windows, it uses the CryptGenRandom API.
//
// Key security properties:
// - Uses the best available system entropy source
// - Provides cryptographically secure randomness suitable for one-time pads
// - Resistant to statistical analysis and prediction attacks
// - Protected against concurrent access with internal locking
//
// Failure modes to monitor:
// - On embedded systems, may block if system entropy is depleted
// - May return errors during OS-level entropy source failures
// - Can experience performance degradation under heavy load
//
// Usage:
// This generator should typically be used as part of a MultiRNG setup
// via the NewDefaultRNG() function, which provides additional redundancy.
type CryptoRand struct {
	// lock protects against concurrent access to the crypto RNG
	lock sync.Mutex
}

// NewCryptoRand creates a crypto/rand based RNG
func NewCryptoRand() *CryptoRand {
	return &CryptoRand{}
}

// Name
func (r *CryptoRand) Name() string {
	return "crypto"
}

// Read implements the RNG interface by using the platform's strongest
// random number generator with context support for logging.
func (r *CryptoRand) Read(ctx context.Context, p []byte) error {
	log := trace.FromContext(ctx).WithPrefix("CRYPTO-RNG")

	r.lock.Lock()
	defer r.lock.Unlock()

	n, err := crand.Read(p)
	if err != nil {
		log.Error(fmt.Errorf("crypto/rand read failed: %w", err))
		return fmt.Errorf("crypto/rand read failed: %w", err)
	}
	if n != len(p) {
		log.Error(fmt.Errorf("crypto/rand read returned %d bytes, expected %d", n, len(p)))
		return fmt.Errorf("crypto/rand read returned %d bytes, expected %d", n, len(p))
	}

	return nil
}

// MathRand is a secondary source of randomness for the padlock system.
//
// This implementation uses Go's math/rand package with a cryptographically
// secure seed obtained from crypto/rand. It serves as a backup source of
// randomness, providing defense in depth in case the primary source experiences
// issues.
//
// Security properties:
// - Initialized with a high-entropy seed from crypto/rand
// - Provides deterministic but unpredictable pseudorandom sequence
// - Protected against concurrent access with internal locking
// - Computationally efficient for generating large amounts of random data
//
// Security limitations:
// - Relies on a good initial seed; compromised seed reduces security
// - Not a cryptographically secure PRNG by itself
// - Output will eventually repeat (though after a very long period)
// - Should never be used as the sole source of randomness
//
// Usage context:
// This generator is included in MultiRNG via NewDefaultRNG() to provide
// additional entropy mixing and redundancy. It is never meant to be used
// standalone for security-critical operations.
type MathRand struct {
	// src is the pseudorandom source
	src *mrand.Rand
	// lock protects against concurrent access to the math RNG
	lock sync.Mutex
}

// NewMathRand creates a math/rand based RNG with a secure seed from crypto/rand.
func NewMathRand() *MathRand {
	var seed int64
	b := make([]byte, 8)
	if _, err := crand.Read(b); err == nil {
		for i := 0; i < 8; i++ {
			seed = (seed << 8) | int64(b[i])
		}
	}
	return &MathRand{
		src: mrand.New(mrand.NewSource(seed)),
	}
}

// Name
func (r *MathRand) Name() string {
	return "math"
}

// Read implements the RNG interface by using a pseudo-random generator
// with a cryptographically secure seed and context support for logging.
func (mr *MathRand) Read(ctx context.Context, p []byte) error {

	mr.lock.Lock()
	defer mr.lock.Unlock()

	for i := range p {
		p[i] = byte(mr.src.Intn(256))
	}

	return nil
}

// ChaCha20Rand implements RNG using the ChaCha20 stream cipher
type ChaCha20Rand struct {
	lock   sync.Mutex
	stream cipher.Stream
	key    []byte
	nonce  []byte
}

// NewChaCha20Rand creates a new ChaCha20-based random number generator
func NewChaCha20Rand() *ChaCha20Rand {
	// Generate a random key and nonce using crypto/rand
	key := make([]byte, chacha20.KeySize)
	nonce := make([]byte, chacha20.NonceSize)

	// We use the crypto/rand package to generate a secure seed
	_, err := crand.Read(key)
	if err != nil {
		panic(fmt.Sprintf("Failed to generate ChaCha20 key: %v", err))
	}

	_, err = crand.Read(nonce)
	if err != nil {
		panic(fmt.Sprintf("Failed to generate ChaCha20 nonce: %v", err))
	}

	stream, err := chacha20.NewUnauthenticatedCipher(key, nonce)
	if err != nil {
		panic(fmt.Sprintf("Failed to create ChaCha20 stream: %v", err))
	}

	return &ChaCha20Rand{
		stream: stream,
		key:    key,
		nonce:  nonce,
	}
}

// Name
func (r *ChaCha20Rand) Name() string {
	return "chacha20"
}

// Read implements the RNG interface by generating random bytes using ChaCha20
func (c *ChaCha20Rand) Read(ctx context.Context, p []byte) error {

	c.lock.Lock()
	defer c.lock.Unlock()

	// Fill with zeros
	for i := range p {
		p[i] = 0
	}

	// XOR with ChaCha20 keystream
	c.stream.XORKeyStream(p, p)

	return nil
}

// PCG64Rand implements RNG using the PCG64 algorithm from math/rand/v2
type PCG64Rand struct {
	lock sync.Mutex
	rng  *rand2.Rand
}

// NewPCG64Rand creates a new PCG64-based random number generator
func NewPCG64Rand() *PCG64Rand {
	// Generate random seed
	var seed [8]byte
	_, err := crand.Read(seed[:])
	if err != nil {
		panic(fmt.Sprintf("Failed to generate PCG64 seed: %v", err))
	}

	// Create a new PCG64 PRNG using the math/rand/v2 package
	// This uses the PCG64 algorithm by default in Go 1.22+
	rng := rand2.New(rand2.NewPCG(
		binary.LittleEndian.Uint64(seed[:]),
		uint64(time.Now().UnixNano()),
	))

	return &PCG64Rand{
		rng: rng,
	}
}

// Name
func (r *PCG64Rand) Name() string {
	return "pcg64"
}

// Read implements the RNG interface by generating random bytes using PCG64
func (p *PCG64Rand) Read(ctx context.Context, b []byte) error {

	p.lock.Lock()
	defer p.lock.Unlock()

	for i := range b {
		b[i] = byte(p.rng.IntN(256))
	}

	return nil
}

// MT19937Rand implements RNG using the Mersenne Twister algorithm
type MT19937Rand struct {
	lock    sync.Mutex
	rng     *mt19937.MT19937
	wrapper *mrand.Rand
}

// NewMT19937Rand creates a new Mersenne Twister-based random number generator
func NewMT19937Rand() *MT19937Rand {
	// Create MT19937 instance
	mt := mt19937.New()

	// Generate random seed
	var seed [8]byte
	_, err := crand.Read(seed[:])
	if err != nil {
		panic(fmt.Sprintf("Failed to generate MT19937 seed: %v", err))
	}

	// Seed the MT19937 instance
	mt.Seed(int64(binary.LittleEndian.Uint64(seed[:])))

	// Create a wrapper for easier usage
	wrapper := mrand.New(mt)

	return &MT19937Rand{
		rng:     mt,
		wrapper: wrapper,
	}
}

// Name
func (r *MT19937Rand) Name() string {
	return "mt19937"
}

// Read implements the RNG interface by generating random bytes using MT19937
func (m *MT19937Rand) Read(ctx context.Context, b []byte) error {

	m.lock.Lock()
	defer m.lock.Unlock()

	for i := range b {
		b[i] = byte(m.wrapper.Intn(256))
	}

	return nil
}
