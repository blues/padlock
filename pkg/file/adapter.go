package file

import (
	"context"
	"fmt"
	"io"
	"math"

	"github.com/blues/padlock/pkg/trace"
)

// ChunkWriter is a simple io.WriteCloser that accumulates data and writes it on close,
// with built-in randomness validation
type ChunkWriter struct {
	ctx       context.Context
	formatter Formatter
	collPath  string
	collIndex int
	chunkNum  int
	chunkData []byte
}

// NewChunkWriter creates a new ChunkWriter for a specific collection and chunk
func NewChunkWriter(ctx context.Context, formatter Formatter, collPath string, collIndex int, chunkNum int) *ChunkWriter {
	return &ChunkWriter{
		ctx:       ctx,
		formatter: formatter,
		collPath:  collPath,
		collIndex: collIndex,
		chunkNum:  chunkNum,
		chunkData: make([]byte, 0),
	}
}

// Write implements io.Writer interface
func (cw *ChunkWriter) Write(p []byte) (n int, err error) {
	cw.chunkData = append(cw.chunkData, p...)
	return len(p), nil
}

// validateRandomness performs basic statistical tests on data to ensure it appears random
// This helps protect users from accidentally writing non-random or low-entropy data
func (cw *ChunkWriter) validateRandomness() error {
	log := trace.FromContext(cw.ctx).WithPrefix("RANDOMNESS-CHECK")

	// Skip validation for very small chunks (less than 32 bytes)
	if len(cw.chunkData) < 32 {
		log.Debugf("Skipping randomness check for small chunk (%d bytes)", len(cw.chunkData))
		return nil
	}

	// Calculate byte frequency distribution
	counts := make([]int, 256)
	for _, b := range cw.chunkData {
		counts[b]++
	}

	// Check for byte distribution anomalies
	// We expect a relatively uniform distribution in random data
	zeroCount := 0
	highByteCount := 0
	zeros := counts[0]

	// Count extreme values
	for _, count := range counts {
		if count == 0 {
			zeroCount++
		}
		if count > len(cw.chunkData)/10 { // More than 10% of data is a single byte value
			highByteCount++
		}
	}

	// Detection parameters - tweaked for reasonable sensitivity without false positives
	// 1. Too many byte values never appear (suggests limited range of values)
	if zeroCount > 128 {
		log.Debugf("Warning: %d out of 256 possible byte values never appear in the data", zeroCount)

		// Don't fail the write, but warn the user through logging
		if zeroCount > 200 {
			log.Infof("⚠️ Low entropy detected: Data has limited byte diversity. Only %d/256 possible byte values used.", 256-zeroCount)
		}
	}

	// 2. Too many of a single byte value (suggests patterns or non-randomness)
	if highByteCount > 5 {
		log.Debugf("Warning: %d byte values appear with unusually high frequency", highByteCount)
		log.Infof("⚠️ Possible non-random pattern detected in data. Some byte values appear with unusually high frequency.")
	}

	// 3. Too many zeros or ones (common in non-random data like all-zero blocks)
	if zeros > len(cw.chunkData)/4 {
		log.Infof("⚠️ Low randomness warning: %d%% of data consists of zero bytes.", 100*zeros/len(cw.chunkData))
	}

	// Calculate byte-level Shannon entropy (scaled 0-8 bits)
	// This is a good overall measurement of randomness/unpredictability
	entropy := 0.0
	dataLen := float64(len(cw.chunkData))
	for _, count := range counts {
		if count > 0 {
			p := float64(count) / dataLen
			entropy -= p * math.Log2(p)
		}
	}

	// Truly random data should have entropy close to 8 bits per byte
	if entropy < 6.5 {
		log.Infof("⚠️ Low entropy warning: Data entropy is %.2f bits per byte (high-quality random data should be close to 8.0)", entropy)
		// While this is concerning, don't block the operation - just warn the user
	} else {
		log.Debugf("Data passed randomness check: entropy = %.2f bits per byte", entropy)
	}

	// Return nil to allow the operation to proceed regardless of warnings
	// This allows valid writes with warnings, but we've alerted the user to potential issues
	return nil
}

// Close implements io.Closer interface
func (cw *ChunkWriter) Close() error {
	// Validate randomness before writing
	if err := cw.validateRandomness(); err != nil {
		log := trace.FromContext(cw.ctx).WithPrefix("CHUNK-WRITER")
		log.Error(fmt.Errorf("randomness validation failed: %w", err))
		// Note: we continue even after validation errors to maintain compatibility
	}

	return cw.formatter.WriteChunk(cw.ctx, cw.collPath, cw.collIndex, cw.chunkNum, cw.chunkData)
}

// ChunkReaderAdapter adapts CollectionReader to io.Reader
type ChunkReaderAdapter struct {
	Reader       *CollectionReader
	buffer       []byte
	offset       int
	ctx          context.Context
	currentChunk int // Track which chunk we're reading
}

// Path returns the path of the underlying collection
func (a *ChunkReaderAdapter) Path() string {
	return a.Reader.Collection.Path
}

// Name returns the name of the underlying collection
func (a *ChunkReaderAdapter) Name() string {
	return a.Reader.Collection.Name
}

// NewChunkReaderAdapter creates a new ChunkReaderAdapter from a CollectionReader
func NewChunkReaderAdapter(ctx context.Context, reader *CollectionReader) *ChunkReaderAdapter {
	return &ChunkReaderAdapter{
		Reader:       reader,
		ctx:          ctx,
		currentChunk: 1, // Start with chunk 1
	}
}

// SetCurrentChunk sets the current chunk index for the adapter
func (a *ChunkReaderAdapter) SetCurrentChunk(chunkIndex int) {
	a.currentChunk = chunkIndex
	// Reset buffer when changing chunks
	a.buffer = nil
	a.offset = 0

	// Also update the reader's chunk index to match
	a.Reader.ChunkIndex = chunkIndex
}

// Read implements io.Reader interface
func (a *ChunkReaderAdapter) Read(p []byte) (int, error) {
	log := trace.FromContext(a.ctx).WithPrefix("CHUNK-READER")

	// If buffer is empty or fully read, get next chunk
	if a.buffer == nil || a.offset >= len(a.buffer) {
		log.Debugf("Getting next chunk from collection %s (chunk %d)",
			a.Reader.Collection.Name, a.currentChunk)

		// Make sure we reset the reader's chunk index to the one we want
		// This ensures we only read one chunk at a time
		a.Reader.ChunkIndex = a.currentChunk

		chunk, err := a.Reader.ReadNextChunk(a.ctx)
		if err != nil {
			if err == io.EOF {
				log.Debugf("Reached end of chunks (EOF) for collection %s", a.Reader.Collection.Name)

				// Increment currentChunk even on EOF so we're ready for the next call
				a.currentChunk++
				// Signal that we've reached the end of this chunk
				return 0, io.EOF
			} else {
				log.Error(fmt.Errorf("error getting chunk %d from collection %s: %w",
					a.currentChunk, a.Reader.Collection.Name, err))
				return 0, err
			}
		}

		// We've successfully read the chunk, increment the chunk number for next time
		a.currentChunk++

		log.Debugf("Got chunk %d (%d bytes) from collection %s",
			a.Reader.ChunkIndex, len(chunk), a.Reader.Collection.Name)

		a.buffer = chunk
		a.offset = 0
	}

	// Copy data from buffer to p
	n := copy(p, a.buffer[a.offset:])
	a.offset += n
	log.Debugf("Read %d bytes from buffer (offset: %d, buffer size: %d)", n, a.offset, len(a.buffer))
	return n, nil
}
