// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

// Package padlock implements the high-level operations for encoding and decoding files
// using the K-of-N threshold one-time-pad cryptographic scheme.
//
// This package serves as the orchestration layer between:
// - The core cryptographic threshold scheme implementation (pkg/pad)
// - The file system operations layer (pkg/file)
// - The command-line interface (cmd/padlock)
//
// The padlock system provides information-theoretic security through:
// - A K-of-N threshold scheme: Any K out of N collections can reconstruct the data
// - One-time pad encryption: Uses truly random keys combined with XOR operations
// - Defense in depth: Multiple independent sources of randomness
// - Serialization: Processes entire directories with optional compression
//
// The key components of this package are:
//
// 1. EncodeDirectory: Splits an input directory into N collections
//   - Validates input/output directories
//   - Creates necessary directories and collections
//   - Serializes input directory to a tar stream
//   - Optionally compresses the data
//   - Processes chunks through the pad encoding
//   - Writes to collections in specified format
//   - Optionally creates ZIP archives for collections
//
// 2. DecodeDirectory: Reconstructs original data from K or more collections
//   - Locates and validates available collections
//   - Handles both directory and ZIP collection formats
//   - Sets up a pipeline for decoding and decompression
//   - Deserializes the decoded stream to output directory
//
// Security considerations:
// - Security depends entirely on the quality of randomness
// - Collections should be stored in separate locations
// - Same collections should never be reused for different data
package padlock

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blues/padlock/pkg/file"
	"github.com/blues/padlock/pkg/pad"
	"github.com/blues/padlock/pkg/trace"
)

// SizeTracker helps track file sizes during encoding and decoding.
// This allows for implementing the -dryrun feature that reports size information
// without actually writing output files.
type SizeTracker struct {
	// InputSize is the total size of the input data in bytes
	InputSize int64

	// CompressedInputSize is the size of the input data after compression
	CompressedInputSize int64

	// EncodeCollectionsTotalSize is the sum of sizes of all collections
	EncodeCollectionsTotalSize int64

	// EncodeCollectionsSizes contains the size of each individual collection
	EncodeCollectionsSizes map[string]int64

	// DecodeOutputSize is the size of the fully expanded output data
	DecodeOutputSize int64
}

// FormatByteSize formats a byte count with thousands separators for better readability
func FormatByteSize(bytes int64) string {
	// Handle negative values
	if bytes < 0 {
		return "-" + FormatByteSize(-bytes)
	}

	// Format number with commas
	str := fmt.Sprintf("%d", bytes)
	result := ""
	for i, ch := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(ch)
	}
	return result
}

// SizeTrackingWriter is an io.Writer implementation that counts bytes without writing them.
// It can be used as a replacement for an actual file writer when only calculating sizes.
type SizeTrackingWriter struct {
	// Size tracks the total bytes "written"
	Size int64

	// CollectionName tracks which collection this writer is for
	CollectionName string

	// SizeTracker reference to update the central size tracker
	Tracker *SizeTracker
}

// NewSizeTrackingWriter creates a new SizeTrackingWriter instance.
func NewSizeTrackingWriter(collectionName string, tracker *SizeTracker) *SizeTrackingWriter {
	return &SizeTrackingWriter{
		Size:           0,
		CollectionName: collectionName,
		Tracker:        tracker,
	}
}

// Write implements the io.Writer interface. Instead of writing to a file,
// it simply counts the bytes and updates the size counters.
func (w *SizeTrackingWriter) Write(p []byte) (n int, err error) {
	size := len(p)
	w.Size += int64(size)

	// Update the collection's size in the tracker if we're tracking a collection
	if w.CollectionName != "" && w.Tracker != nil {
		if w.Tracker.EncodeCollectionsSizes == nil {
			w.Tracker.EncodeCollectionsSizes = make(map[string]int64)
		}
		w.Tracker.EncodeCollectionsSizes[w.CollectionName] += int64(size)
		w.Tracker.EncodeCollectionsTotalSize += int64(size)
	}

	return size, nil
}

// Close implements the io.Closer interface.
func (w *SizeTrackingWriter) Close() error {
	return nil
}

// SizeTrackingWriteCloser is an interface combining both io.Writer and io.Closer.
type SizeTrackingWriteCloser interface {
	io.Writer
	io.Closer
}

// SizeTrackingReader wraps an io.Reader to count bytes as they're read.
// This helps track the size of input/output streams during the size-only operation.
type SizeTrackingReader struct {
	Reader  io.Reader
	Size    int64
	Tracker *SizeTracker
	IsInput bool // Whether this is tracking input (true) or output (false)
}

// NewSizeTrackingReader creates a new SizeTrackingReader instance.
func NewSizeTrackingReader(reader io.Reader, tracker *SizeTracker, isInput bool) *SizeTrackingReader {
	return &SizeTrackingReader{
		Reader:  reader,
		Size:    0,
		Tracker: tracker,
		IsInput: isInput,
	}
}

// Read implements the io.Reader interface. It proxies reads to the underlying reader
// while counting the bytes that pass through.
func (r *SizeTrackingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.Size += int64(n)

	// Update the appropriate tracker field based on whether this is input or output
	if r.Tracker != nil {
		if r.IsInput {
			r.Tracker.InputSize = r.Size
		} else {
			r.Tracker.DecodeOutputSize = r.Size
		}
	}

	return n, err
}

// Close implements the io.Closer interface if the underlying reader supports it.
func (r *SizeTrackingReader) Close() error {
	if closer, ok := r.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// SizeTrackingReadCloser combines io.Reader and io.Closer.
type SizeTrackingReadCloser interface {
	io.Reader
	io.Closer
}

// compressForDryRun performs a complete in-memory compression of the input data
// to accurately measure the size of compressed data during a dry run.
func compressForDryRun(ctx context.Context, inputStream io.Reader, sizeTracker *SizeTracker) (io.Reader, error) {
	log := trace.FromContext(ctx).WithPrefix("padlock")

	// Read all the uncompressed data
	uncompressedData, err := io.ReadAll(inputStream)
	if err != nil {
		log.Error(fmt.Errorf("failed to read input data: %w", err))
		return nil, err
	}

	// Store the uncompressed size
	sizeTracker.InputSize = int64(len(uncompressedData))
	log.Debugf("Uncompressed input size: %d bytes", sizeTracker.InputSize)

	// Create a buffer for compressed data
	var compressedBuf bytes.Buffer

	// Compress the data
	gzw := gzip.NewWriter(&compressedBuf)
	_, err = gzw.Write(uncompressedData)
	if err != nil {
		log.Error(fmt.Errorf("failed to compress data: %w", err))
		return nil, err
	}

	// Close the gzip writer to flush any remaining data
	if err := gzw.Close(); err != nil {
		log.Error(fmt.Errorf("failed to close gzip writer: %w", err))
		return nil, err
	}

	// Store the compressed size
	sizeTracker.CompressedInputSize = int64(compressedBuf.Len())
	log.Debugf("Compressed input size: %d bytes", sizeTracker.CompressedInputSize)

	// Return a reader for the compressed data
	return bytes.NewReader(compressedBuf.Bytes()), nil
}

// Format is a type alias for file.Format, representing the output format for collections.
// A Format determines how data chunks are written to and read from the filesystem.
type Format = file.Format

// Compression represents the compression mode used when serializing directories.
// This allows for space-efficient storage while maintaining the security properties
// of the threshold scheme.
type Compression int

const (
	// FormatBin is a binary format that stores data chunks directly as binary files.
	// This format is more efficient but less portable across different systems.
	FormatBin = file.FormatBin

	// FormatPNG is a PNG format that stores data chunks as images.
	// This format is useful for cases where binary files might be altered by
	// transfer systems, or where visual confirmation of collection existence is helpful.
	FormatPNG = file.FormatPNG

	// CompressionNone indicates no compression will be applied to the serialized data.
	// Use this when processing already compressed data or when processing speed is critical.
	CompressionNone Compression = iota

	// CompressionGzip indicates gzip compression will be applied to reduce storage requirements.
	// This is the default compression mode, providing good compression ratios with reasonable speed.
	CompressionGzip
)

// EncodeConfig holds configuration parameters for the encoding operation.
// This structure is created by the command-line interface and passed to EncodeDirectory.
type EncodeConfig struct {
	InputDir           string      // Path to the directory containing data to encode
	OutputDir          string      // Path where the encoded collections will be created (for backward compatibility)
	OutputDirs         []string    // List of output directories, one for each collection when multiple dirs are specified
	N                  int         // Total number of collections to create (N value)
	K                  int         // Minimum collections required for reconstruction (K value)
	Format             Format      // Output format (binary or PNG)
	ChunkSize          int         // Maximum size for data chunks in bytes
	RNG                pad.RNG     // Random number generator for one-time pad creation
	ClearIfNotEmpty    bool        // Whether to clear the output directory if not empty
	Verbose            bool        // Enable verbose logging
	Compression        Compression // Compression mode for the serialized data
	ArchiveCollections bool        // Whether to create TAR archives for collections
	SizeOnly           bool        // Whether to only calculate sizes without writing output files (dryrun mode)
}

// DecodeConfig holds configuration parameters for the decoding operation.
// This structure is created by the command-line interface and passed to DecodeDirectory.
type DecodeConfig struct {
	InputDir        string      // Path to the directory containing collections to decode (for backward compatibility)
	InputDirs       []string    // List of input directories, each containing a collection to decode
	OutputDir       string      // Path where the decoded data will be written
	RNG             pad.RNG     // Random number generator (unused for decoding, but maintained for consistency)
	Verbose         bool        // Enable verbose logging
	Compression     Compression // Compression mode used when the data was encoded
	ClearIfNotEmpty bool        // Whether to clear the output directory if not empty
	SizeOnly        bool        // Whether to only calculate sizes without writing output files (dryrun mode)
}

// EncodeDirectory encodes a directory using the padlock K-of-N threshold scheme.
//
// This function orchestrates the entire encoding process:
// 1. Validates the input and output directories
// 2. Creates the cryptographic pad with specified K-of-N parameters
// 3. Sets up the collection directories where encoded data will be written
// 4. Serializes the input directory to a tar stream
// 5. Optionally compresses the serialized data
// 6. Processes the data through the one-time pad encoder in chunks
// 7. Distributes encoded chunks across the collections
// 8. Optionally creates ZIP archives for easy distribution
//
// Parameters:
//   - ctx: Context with logging, cancellation, and tracing capabilities
//   - cfg: Configuration parameters for the encoding operation
//
// Returns:
//   - An error if any part of the encoding process fails, nil on success
//
// The encoding process ensures that the resulting collections have the following property:
// Any K or more collections can be used to reconstruct the original data, while
// K-1 or fewer collections reveal absolutely nothing about the original data.
func EncodeDirectory(ctx context.Context, cfg EncodeConfig) error {
	log := trace.FromContext(ctx).WithPrefix("padlock")
	start := time.Now()

	// Log differently depending on whether using single or multiple output directories
	if len(cfg.OutputDirs) <= 1 {
		log.Infof("Starting encode: InputDir=%s OutputDir=%s", cfg.InputDir, cfg.OutputDir)
	} else {
		log.Infof("Starting encode: InputDir=%s with %d output directories", cfg.InputDir, len(cfg.OutputDirs))
		for i, dir := range cfg.OutputDirs {
			log.Debugf("  OutputDir[%d]=%s", i, dir)
		}
	}
	log.Debugf("Encode parameters: copies=%d, required=%d, Format=%s, ChunkSize=%d", cfg.N, cfg.K, cfg.Format, cfg.ChunkSize)

	// Validate input directory to ensure it exists and is accessible
	if err := file.ValidateInputDirectory(ctx, cfg.InputDir); err != nil {
		return err
	}

	// In dry run mode, we don't need to prepare output directories
	if !cfg.SizeOnly {
		// Prepare all output directories, clearing them if requested and they're not empty
		if len(cfg.OutputDirs) > 1 {
			// When using multiple output directories - prepare each one individually
			for _, dir := range cfg.OutputDirs {
				if err := file.PrepareOutputDirectory(ctx, dir, cfg.ClearIfNotEmpty); err != nil {
					return err
				}
			}
		} else {
			// Traditional single output directory approach
			if err := file.PrepareOutputDirectory(ctx, cfg.OutputDir, cfg.ClearIfNotEmpty); err != nil {
				return err
			}
		}
	} else {
		log.Infof("Running in dry run mode - skipping output directory preparation")
	}

	// Create a new pad instance with the specified N and K parameters
	// This is the core cryptographic component that implements the threshold scheme
	log.Debugf("Creating pad instance with N=%d, K=%d", cfg.N, cfg.K)
	p, err := pad.NewPadForEncode(ctx, cfg.N, cfg.K)
	if err != nil {
		log.Error(fmt.Errorf("failed to create pad instance: %w", err))
		return err
	}

	// Initialize size tracker if we're in size-only mode
	var sizeTracker *SizeTracker
	if cfg.SizeOnly {
		sizeTracker = &SizeTracker{
			InputSize:                  0,
			CompressedInputSize:        0,
			EncodeCollectionsTotalSize: 0,
			EncodeCollectionsSizes:     make(map[string]int64),
			DecodeOutputSize:           0,
		}
		p.SizeTracker = sizeTracker
	}

	// Create collections based on the configuration
	var collections []file.Collection

	// In dry run mode, we don't need to actually create collection directories
	if cfg.SizeOnly {
		// Just set up virtual collections for dry run
		collections = make([]file.Collection, len(p.Collections))
		for i, collName := range p.Collections {
			collections[i] = file.Collection{
				Name:   collName,
				Path:   "dryrun-" + collName, // Use a placeholder path
				Format: cfg.Format,
			}
			log.Debugf("Created virtual collection %d for dry run: %s", i+1, collName)
		}
	} else if len(cfg.OutputDirs) > 1 {
		// Use multiple output directories - one collection per directory
		if len(cfg.OutputDirs) != len(p.Collections) {
			return fmt.Errorf("number of output directories (%d) does not match number of collections (%d)",
				len(cfg.OutputDirs), len(p.Collections))
		}

		// Create collections in individual directories
		collections = make([]file.Collection, len(p.Collections))
		for i, collName := range p.Collections {
			// For multiple output dirs, we use the actual directory as the collection directory
			// (not a subdirectory like in the traditional approach)
			collections[i] = file.Collection{
				Name:   collName,
				Path:   cfg.OutputDirs[i],
				Format: cfg.Format,
			}
			log.Debugf("Created collection %d: %s at %s", i+1, collName, cfg.OutputDirs[i])
		}
	} else if !cfg.ArchiveCollections {
		// For directory-based output, create collection subdirectories
		var err error
		collections, err = file.CreateCollections(ctx, cfg.OutputDir, p.Collections)
		if err != nil {
			return err
		}

		// Set format for all collections
		for i := range collections {
			collections[i].Format = cfg.Format
		}
	} else {
		// For TAR-based output in a single directory, just create collection references
		// without actually creating directories (we'll write directly to TAR files)
		collections = make([]file.Collection, len(p.Collections))
		for i, collName := range p.Collections {
			collections[i] = file.Collection{
				Name:   collName,
				Path:   filepath.Join(cfg.OutputDir, collName),
				Format: cfg.Format,
			}
			log.Debugf("Created virtual collection %d: %s at %s", i+1, collName, collections[i].Path)
		}
	}

	// Get the formatter for the specified format (binary or PNG)
	// This determines how data chunks are written to and read from disk
	formatter := file.GetFormatter(cfg.Format)

	// Create a tar stream from the input directory
	// This serializes all files and directories into a single stream for processing
	log.Debugf("Creating tar stream from input directory: %s", cfg.InputDir)
	tarStream, err := file.SerializeDirectoryToStream(ctx, cfg.InputDir)
	if err != nil {
		log.Error(fmt.Errorf("failed to create tar stream: %w", err))
		return fmt.Errorf("failed to create tar stream: %w", err)
	}
	defer tarStream.Close()

	// Add compression if configured (typically GZIP)
	// This reduces storage requirements without affecting security
	var inputStream io.Reader = tarStream
	if cfg.Compression == CompressionGzip {
		log.Debugf("Adding gzip compression to stream")

		// If we're in size-only mode, use in-memory compression to track sizes accurately
		if cfg.SizeOnly && sizeTracker != nil {
			var err error
			inputStream, err = compressForDryRun(ctx, tarStream, sizeTracker)
			if err != nil {
				log.Error(fmt.Errorf("failed to compress for dry run: %w", err))
				return fmt.Errorf("failed to compress for dry run: %w", err)
			}
		} else {
			inputStream = file.CompressStreamToStream(ctx, tarStream)
		}
	}

	// Define a callback function that creates chunk writers for the encoding process
	// Each time the pad encoder needs to write a chunk, this function is called
	//
	// When archive collections is enabled, this will create TarChunkWriters to write
	// chunks directly to TAR files instead of temporary files on disk.
	newChunkFunc := func(collectionName string, chunkNumber int, chunkFormat string) (io.WriteCloser, error) {
		// If in size-only mode, use SizeTrackingWriter instead of actual file writers
		if cfg.SizeOnly && sizeTracker != nil {
			return NewSizeTrackingWriter(collectionName, sizeTracker), nil
		}

		// Find the collection path for the given collection name
		var collPath string
		var found bool

		for _, c := range collections {
			if c.Name == collectionName {
				collPath = c.Path
				found = true
				break
			}
		}

		if !found || collPath == "" {
			return nil, fmt.Errorf("collection not found: %s", collectionName)
		}

		// If archive collections is enabled, create TarChunkWriter
		if cfg.ArchiveCollections {
			// Handle TAR path differently based on single vs multiple output dirs
			var tarPath string

			if len(cfg.OutputDirs) > 1 {
				// For multiple output directories, put the TAR inside the directory
				tarPath = filepath.Join(collPath, collectionName+".tar")
			} else {
				// For single output directory, put TAR next to the collection directory
				tarPath = collPath
				if !strings.HasSuffix(tarPath, ".tar") {
					tarPath = tarPath + ".tar"
				}
			}

			log.Debugf("Preparing to write to TAR file at: %s", tarPath)

			// Create the TarChunkWriter for this chunk if it doesn't exist yet
			tarWriter, err := file.NewTarChunkWriter(ctx, tarPath, collectionName, cfg.Format)
			if err != nil {
				return nil, fmt.Errorf("failed to create tar chunk writer: %w", err)
			}

			// Set the chunk number for this write operation
			tarWriter.ChunkNum = chunkNumber

			return tarWriter, nil
		}

		// Otherwise use the standard NamedChunkWriter for directory output
		return &file.NamedChunkWriter{
			Ctx:       ctx,
			Formatter: formatter,
			CollPath:  collPath,
			CollName:  collectionName,
			ChunkNum:  chunkNumber,
		}, nil
	}

	// Run the actual encoding process, which:
	// 1. Reads data from the input stream in chunks
	// 2. Generates random one-time pads for each chunk
	// 3. XORs input data with pads to create ciphertext
	// 4. Distributes the results across collections according to the threshold scheme
	log.Debugf("Starting encode process with chunk size: %d", cfg.ChunkSize)
	err = p.Encode(
		ctx,
		cfg.ChunkSize,
		inputStream,
		cfg.RNG,
		newChunkFunc,
		string(cfg.Format),
	)
	if err != nil {
		log.Error(fmt.Errorf("encoding failed: %w", err))
		return fmt.Errorf("encoding failed: %w", err)
	}

	// Skip archive finalization in dry run mode
	if cfg.SizeOnly {
		log.Debugf("Skipping archive finalization in dry run mode")
	} else if cfg.ArchiveCollections {
		// If archives were enabled, the chunks have already been written directly to TAR files
		// We need to finalize the TAR writers to ensure they're properly closed
		// Finalize all TAR writers to ensure proper closing
		log.Debugf("Finalizing all TAR writers created during encoding")
		if err := file.FinalizeAllTarWriters(ctx); err != nil {
			log.Error(fmt.Errorf("failed to finalize TAR writers: %w", err))
			return err
		}
		log.Debugf("All TAR writers finalized successfully")

		// For single output directory, we might have empty directories to clean up
		// but for multiple output directories, we should leave directories alone
		if len(cfg.OutputDirs) <= 1 {
			log.Debugf("Cleaning up empty collection directories after creating TAR files")
			for _, coll := range collections {
				// Only remove if it's a directory and not a TAR file
				if !strings.HasSuffix(coll.Path, ".tar") {
					info, err := os.Stat(coll.Path)
					if err == nil && info.IsDir() {
						if err := os.RemoveAll(coll.Path); err != nil {
							log.Debugf("Warning: Failed to remove collection directory: %s (%v)", coll.Path, err)
						} else {
							log.Debugf("Removed collection directory: %s", coll.Path)
						}
					}
				}
			}
		}
	} else if len(cfg.OutputDirs) > 1 && !cfg.ArchiveCollections {
		// For multiple output directories with files mode, do nothing extra
		// Just leave the files in the directories as they were created
		log.Debugf("Using files mode with multiple directories - keeping files in place")
	} else if len(cfg.OutputDirs) > 1 {
		// For multiple output directories with archive mode, create tar archives in each directory
		// but don't delete the directories (just archive the contents)
		for _, coll := range collections {
			tarPath, err := file.TarDirectoryContents(ctx, coll.Path, coll.Name)
			if err != nil {
				log.Error(fmt.Errorf("failed to create tar archive for collection %s: %w", coll.Name, err))
				return err
			}
			log.Infof("Created tar archive for collection %s: %s", coll.Name, tarPath)
		}
	} else if !cfg.ArchiveCollections {
		// For single output directory with files mode, do nothing extra
		// Just leave the files in the directories as they were created
		log.Debugf("Using files mode with single directory - keeping files in place")
	} else {
		// Traditional approach for single output directory with archive mode
		// Create TAR files and delete the directories
		if _, err := file.TarCollections(ctx, collections); err != nil {
			return err
		}
	}

	// Perform verification for PNG collections if not in dry run mode
	if !cfg.SizeOnly && cfg.Format == FormatPNG {
		log.Infof("Starting verification pass to ensure PNG data integrity...")

		// If we're using TAR archives, the collection paths need to be updated to point to the TAR files
		if cfg.ArchiveCollections {
			for i := range collections {
				if !strings.HasSuffix(collections[i].Path, ".tar") {
					// For multiple output directories, the TAR files are named differently (collection name inside the dir)
					if len(cfg.OutputDirs) > 1 {
						collections[i].Path = filepath.Join(collections[i].Path, collections[i].Name+".tar")
					} else {
						collections[i].Path = collections[i].Path + ".tar"
					}
				}
			}
		}

		if err := VerifyCollectionIntegrity(ctx, collections, cfg.Format); err != nil {
			log.Error(fmt.Errorf("verification completed with errors: %w", err))
			// We continue despite errors - we want to return the encoded data anyway
		} else {
			log.Infof("Verification completed successfully - all PNG files passed integrity checks")
		}
	}

	// Log completion information including elapsed time
	elapsed := time.Since(start)

	// Display dry run information if in size-only mode
	if cfg.SizeOnly && sizeTracker != nil {
		// Output the size report with asterisk lines at beginning and end
		log.Infof("*** DRY RUN SIZE REPORT ***")

		log.Infof("Original input size:              %s bytes", FormatByteSize(sizeTracker.InputSize))

		if cfg.Compression == CompressionGzip && sizeTracker.CompressedInputSize > 0 {
			log.Infof("Compressed input size:            %s bytes", FormatByteSize(sizeTracker.CompressedInputSize))

			// Calculate compression ratio
			compressionRatio := 0.0
			if sizeTracker.InputSize > 0 {
				compressionRatio = float64(sizeTracker.CompressedInputSize) / float64(sizeTracker.InputSize) * 100.0
			}
			log.Infof("Compression ratio:                %.2f%%", compressionRatio)
		}

		if sizeTracker.EncodeCollectionsTotalSize > 0 {
			// Calculate each collection size as an integer (all collections are same size)
			eachCollectionSize := int64(0)
			if len(sizeTracker.EncodeCollectionsSizes) > 0 {
				eachCollectionSize = sizeTracker.EncodeCollectionsTotalSize / int64(len(sizeTracker.EncodeCollectionsSizes))
			}

			log.Infof("Each collection size:             %s bytes", FormatByteSize(eachCollectionSize))
			log.Infof("Total size of all collections:    %s bytes", FormatByteSize(sizeTracker.EncodeCollectionsTotalSize))

			// Calculate expansion ratio (total collections size / original input size)
			expansionRatio := 0.0
			if sizeTracker.InputSize > 0 {
				expansionRatio = float64(sizeTracker.EncodeCollectionsTotalSize) / float64(sizeTracker.InputSize) * 100.0
			}
			log.Infof("Expansion ratio:                  %.2f%%", expansionRatio)
		}

		// End the report with an asterisk line
		log.Infof("***")
	}

	// Log differently depending on whether using single or multiple output directories
	if len(cfg.OutputDirs) <= 1 {
		log.Infof("Encode complete (%s) -copies %d -required %d -format %s", elapsed, cfg.N, cfg.K, cfg.Format)
	} else {
		log.Infof("Encode complete (%s) with %d output directories -required %d -format %s",
			elapsed, len(cfg.OutputDirs), cfg.K, cfg.Format)
	}

	return nil
}

// isValidCollectionDir checks if a directory is likely to contain a valid collection
func isValidCollectionDir(ctx context.Context, dirPath string) bool {
	log := trace.FromContext(ctx).WithPrefix("padlock")
	log.Debugf("Checking if %s is a valid collection directory", dirPath)

	// Try to determine collection format
	format, err := file.DetermineCollectionFormat(dirPath)
	if err != nil {
		log.Debugf("%s is not a valid collection directory: %v", dirPath, err)
		return false
	}

	log.Debugf("%s appears to be a valid collection directory with format %s", dirPath, format)
	return true
}

// determineCollectionNameFromContent tries to deduce the collection name by examining files
func determineCollectionNameFromContent(ctx context.Context, dirPath string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("padlock")

	// Read the directory
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	// Look for files with pattern like "IMG3A5_0001.PNG" or "3A5_0001.bin"
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Check for PNG files
		if strings.HasSuffix(strings.ToUpper(name), ".PNG") && strings.HasPrefix(name, "IMG") {
			// Extract the collection name after "IMG" and before "_"
			parts := strings.Split(strings.TrimPrefix(name, "IMG"), "_")
			if len(parts) > 0 && file.IsCollectionName(parts[0]) {
				log.Debugf("Determined collection name '%s' from file %s", parts[0], name)
				return parts[0], nil
			}
		}

		// Check for bin files
		if strings.HasSuffix(name, ".bin") {
			// Extract the collection name before "_"
			parts := strings.Split(name, "_")
			if len(parts) > 0 && file.IsCollectionName(parts[0]) {
				log.Debugf("Determined collection name '%s' from file %s", parts[0], name)
				return parts[0], nil
			}
		}
	}

	return "", fmt.Errorf("could not determine collection name from directory content")
}

// DecodeDirectory reconstructs original data from K or more collections using the padlock scheme.
//
// This function orchestrates the entire decoding process:
// 1. Validates the input and output directories
// 2. Locates and loads available collections (from directories or ZIP files)
// 3. Creates readers for each collection to access the encoded chunks
// 4. Sets up a parallel deserialization pipeline using goroutines
// 5. Creates the pad instance for decoding based on available collections
// 6. Processes the collections through the one-time pad decoder
// 7. Deserializes the decoded data to the output directory
//
// Parameters:
//   - ctx: Context with logging, cancellation, and tracing capabilities
//   - cfg: Configuration parameters for the decoding operation
//
// Returns:
//   - An error if any part of the decoding process fails, nil on success
//
// The decoding process can succeed only if at least K collections from the original
// N collections are provided. With fewer than K collections, the function will fail
// and no information about the original data can be recovered due to the information-theoretic
// security properties of the threshold scheme.
func DecodeDirectory(ctx context.Context, cfg DecodeConfig) error {
	log := trace.FromContext(ctx).WithPrefix("padlock")
	start := time.Now()

	// Log differently depending on whether using single or multiple input directories
	if len(cfg.InputDirs) <= 1 {
		log.Infof("Starting decode: InputDir=%s OutputDir=%s", cfg.InputDir, cfg.OutputDir)
	} else {
		log.Infof("Starting decode with %d input directories, OutputDir=%s", len(cfg.InputDirs), cfg.OutputDir)
		for i, dir := range cfg.InputDirs {
			log.Debugf("  InputDir[%d]=%s", i, dir)
		}
	}

	// In dry run mode, we don't need to prepare output directories
	if !cfg.SizeOnly {
		// Prepare the output directory, clearing it if requested and it's not empty
		if err := file.PrepareOutputDirectory(ctx, cfg.OutputDir, cfg.ClearIfNotEmpty); err != nil {
			return err
		}
	} else {
		log.Infof("Running in dry run mode - skipping output directory preparation")
	}

	// Variable to hold all collected collections and a tempDir if needed
	var allCollections []file.Collection
	var collTempDir string

	// Handle single input dir or multiple input dirs
	if len(cfg.InputDirs) <= 1 {
		// Traditional approach - single input directory containing multiple collections
		// Validate input directory to ensure it exists and is accessible
		if err := file.ValidateInputDirectory(ctx, cfg.InputDir); err != nil {
			return err
		}

		// Find collections (directories or zips) in the input directory
		// This identifies all available collections, extracting ZIP files if necessary
		collections, tempDir, err := file.FindCollections(ctx, cfg.InputDir)
		if err != nil {
			return err
		}

		// Use the results
		allCollections = collections
		collTempDir = tempDir
	} else {
		// Multiple input directory mode - each input directory is treated as a collection
		for _, inputDir := range cfg.InputDirs {
			// Validate each input directory
			if err := file.ValidateInputDirectory(ctx, inputDir); err != nil {
				return err
			}

			// First check if this directory contains a collection directly
			// (it might be a directory containing a collection like '3A5')
			if isValidCollectionDir(ctx, inputDir) {
				// The directory itself is a valid collection
				format, err := file.DetermineCollectionFormat(inputDir)
				if err != nil {
					log.Infof("Could not determine collection format for %s, skipping: %v", inputDir, err)
					continue
				}

				collName := filepath.Base(inputDir)
				if !file.IsCollectionName(collName) {
					// If the directory name is not a valid collection name,
					// try to find a valid collection inside by examining files
					collName, err = determineCollectionNameFromContent(ctx, inputDir)
					if err != nil {
						log.Infof("Could not determine collection name for %s, skipping: %v", inputDir, err)
						continue
					}
				}

				collection := file.Collection{
					Name:   collName,
					Path:   inputDir,
					Format: format,
				}
				allCollections = append(allCollections, collection)
				log.Debugf("Found direct collection in %s, name=%s, format=%s", inputDir, collName, format)
			} else {
				// Check if the directory contains collections or zip files
				collections, tempDir, err := file.FindCollections(ctx, inputDir)
				if err != nil {
					log.Infof("Failed to find collections in %s: %v", inputDir, err)
					continue
				}

				// Add these collections to our master list
				allCollections = append(allCollections, collections...)

				// Remember the tempDir for cleanup if it exists
				if tempDir != "" && collTempDir == "" {
					collTempDir = tempDir
				}

				log.Debugf("Found %d collections in directory %s", len(collections), inputDir)
			}
		}
	}

	// If we extracted zip files, clean up the temporary directory when done
	if collTempDir != "" {
		defer func() {
			log.Debugf("Cleaning up temporary directory: %s", collTempDir)
			os.RemoveAll(collTempDir)
		}()
	}

	// Ensure we found at least some collections
	if len(allCollections) == 0 {
		if len(cfg.InputDirs) <= 1 {
			log.Error(fmt.Errorf("no collections found in input directory"))
			return fmt.Errorf("no collections found in input directory")
		} else {
			log.Error(fmt.Errorf("no valid collections found in any of the input directories"))
			return fmt.Errorf("no valid collections found in any of the input directories")
		}
	}
	log.Debugf("Found total of %d collections", len(allCollections))

	// Create collection readers for each collection
	// These readers handle the format-specific details of reading chunks
	readers := make([]io.Reader, len(allCollections))
	collReaders := make([]*file.CollectionReader, len(allCollections))

	for i, coll := range allCollections {
		collReader := file.NewCollectionReader(coll)
		collReaders[i] = collReader

		// Create an adapter that converts the CollectionReader to an io.Reader
		// This adapter handles the details of reading chunks sequentially
		readers[i] = file.NewChunkReaderAdapter(ctx, collReader)
	}

	// Get the number of available collections (important for pad initialization)
	n := len(allCollections)
	log.Infof("Collections: %d", n)

	// Create a new pad instance for decoding
	// The pad is initialized with the number of available collections
	// The K value will be extracted from the collection metadata during decoding
	log.Debugf("Creating pad instance with N=%d", n)
	p, err := pad.NewPadForDecode(ctx, n)
	if err != nil {
		log.Error(fmt.Errorf("failed to create pad instance: %w", err))
		return err
	}

	// Initialize size tracker if we're in size-only mode
	var sizeTracker *SizeTracker
	if cfg.SizeOnly {
		sizeTracker = &SizeTracker{
			InputSize:                  0,
			CompressedInputSize:        0,
			EncodeCollectionsTotalSize: 0,
			EncodeCollectionsSizes:     make(map[string]int64),
			DecodeOutputSize:           0,
		}
		p.SizeTracker = sizeTracker
	}

	// Create a pipe for transferring decoded data between goroutines
	// This allows parallel processing of decoding and deserialization
	log.Debugf("Creating pipe for decoded data")
	pr, pw := io.Pipe()

	// Channel to signal completion of the deserialization goroutine
	done := make(chan struct{})

	// Start the deserialization process in a separate goroutine
	// This goroutine reads from the pipe and writes to the output directory
	var deserializeErr error
	go func() {
		defer close(done) // Signal completion via the done channel
		defer pr.Close()  // Ensure pipe reader is closed when this goroutine exits

		deserializeCtx := trace.WithContext(ctx, log.WithPrefix("deserialize"))

		// Create decompression stream if needed
		// This reverses any compression applied during encoding
		var outputStream io.Reader = pr
		if cfg.Compression == CompressionGzip {
			log.Debugf("Creating decompression stream")
			var err error
			outputStream, err = file.DecompressStreamToStream(deserializeCtx, pr)
			if err != nil {
				log.Error(fmt.Errorf("failed to create decompression stream: %w", err))
				deserializeErr = err
				return
			}
		}

		// Deserialize the tar stream to the output directory
		// This reconstructs the original directory structure and files
		log.Debugf("Deserializing to output directory: %s", cfg.OutputDir)

		// If we're in dry run mode, wrap the output stream with a size tracker
		// and just read through the data without writing to disk
		if cfg.SizeOnly && sizeTracker != nil {
			log.Debugf("Performing dry run size tracking without writing files")

			// Wrap the output stream with our size tracker
			trackingReader := NewSizeTrackingReader(outputStream, sizeTracker, false) // false = output stream

			// Just read through the entire stream to count bytes, but don't write to disk
			_, err := io.Copy(io.Discard, trackingReader)
			if err != nil {
				log.Error(fmt.Errorf("failed to read output stream for size tracking: %w", err))
				deserializeErr = err
			}
		} else {
			// Normal processing mode - actually deserialize to disk
			err := file.DeserializeDirectoryFromStream(deserializeCtx, cfg.OutputDir, outputStream, cfg.ClearIfNotEmpty)
			if err != nil {
				// Special case: Don't treat "too small" tar file as an error for small inputs
				if strings.Contains(err.Error(), "too small to be a valid tar file") {
					log.Infof("Input data appears to be a small raw file rather than a tar archive")
				} else {
					log.Error(fmt.Errorf("failed to deserialize directory: %w", err))
					deserializeErr = err
				}
			}
		}
	}()

	// Run the decoding process
	log.Debugf("Starting decode process")

	// Create collection names list for logging purposes
	collectionNames := make([]string, len(allCollections))
	for i, coll := range allCollections {
		collectionNames[i] = coll.Name
	}

	// Decode the collections
	// This combines the chunks from different collections using the threshold scheme
	// The result is written to the pipe writer (pw)
	err = p.Decode(ctx, readers, pw)
	if err != nil {
		// Enhanced error handling for the unexpected EOF error
		if err == io.ErrUnexpectedEOF || err.Error() == "unexpected EOF" {
			log.Error(fmt.Errorf("decode failed with unexpected EOF - this is typically caused by corrupt PNG files or incomplete collections: %w", err))

			// Provide more detailed troubleshooting information
			log.Infof("Troubleshooting suggestions:")
			log.Infof("1. Ensure all collection files are intact and not corrupted")
			log.Infof("2. Verify you have at least K complete collections out of the original N")
			log.Infof("3. Check if all chunks in the collections have matching chunk numbers")
			log.Infof("4. Try using a different combination of K collections if more are available")

			return fmt.Errorf("decode failed: unexpected EOF - one or more collections may be corrupt or incomplete: %w", err)
		} else {
			log.Error(fmt.Errorf("decoding failed: %w", err))
			return fmt.Errorf("decoding failed: %w", err)
		}
	}

	// Close the pipe writer to signal the end of data to the deserialization goroutine
	err = pw.Close()
	if err != nil {
		log.Error(fmt.Errorf("error closing pipe writer: %w", err))
		// Continue anyway, as the pipe might already be closed by the deserialization goroutine
	}

	// Determine appropriate timeout duration based on environment
	timeoutDuration := getTimeoutDuration(ctx)

	select {
	case <-done:
		log.Debugf("Deserialization goroutine completed")
	case <-time.After(timeoutDuration):
		// Avoid panic on pipe error
		pw.CloseWithError(fmt.Errorf("timeout waiting for deserialization to complete"))
		log.Error(fmt.Errorf("timeout waiting for deserialization to complete after %v", timeoutDuration))
		return fmt.Errorf("timeout waiting for deserialization to complete after %v", timeoutDuration)
	}

	// Check if there was an error in the deserialization
	if deserializeErr != nil {
		return deserializeErr
	}

	// Log completion information including elapsed time
	elapsed := time.Since(start)

	// Display dry run information if in size-only mode
	if cfg.SizeOnly && sizeTracker != nil {
		// Output the size report with asterisk lines at beginning and end
		log.Infof("*** DRY RUN SIZE REPORT ***")

		// Calculate and report total size of input collections
		totalInputSize := int64(0)

		// Calculate total collection size by checking actual files
		for _, inputDir := range cfg.InputDirs {
			// Check if it's a directory or a file
			fileInfo, err := os.Stat(inputDir)
			if err == nil {
				if fileInfo.IsDir() {
					// Sum up all files in the directory
					err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
						if err != nil {
							return nil // skip on error
						}
						if !info.IsDir() {
							totalInputSize += info.Size()
						}
						return nil
					})
					if err != nil {
						log.Debugf("Error walking directory %s: %v", inputDir, err)
					}
				} else {
					// It's a single file
					totalInputSize += fileInfo.Size()
				}
			}
		}

		log.Infof("Total size of input collections:  %s bytes", FormatByteSize(totalInputSize))

		// Report output size if available
		if sizeTracker.DecodeOutputSize > 0 {
			log.Infof("Decompressed output size:        %s bytes", FormatByteSize(sizeTracker.DecodeOutputSize))
		}

		// End the report with an asterisk line
		log.Infof("***")
	}

	log.Infof("Decode complete (%s)", elapsed)
	return nil
}

// VerifyCollectionIntegrity performs a verification pass on all collections to ensure data integrity
// For PNG collections, this verifies each chunk's CRC to detect any corruption
func VerifyCollectionIntegrity(ctx context.Context, collections []file.Collection, format Format) error {
	log := trace.FromContext(ctx).WithPrefix("verify")

	// If not PNG format, verification is not needed
	if format != FormatPNG {
		log.Debugf("Verification only needed for PNG format, skipping for %s format", format)
		return nil
	}

	// Count of chunks verified across all collections
	totalFiles := 0
	totalVerified := 0
	totalErrors := 0
	dotPrinted := false

	// Process each collection
	for i, coll := range collections {
		collLog := log.WithPrefix(fmt.Sprintf("verify-%s", coll.Name))
		collLog.Infof("verifying collection %s (%d of %d)...", coll.Name, i+1, len(collections))

		// Collection-level counts
		collFiles := 0
		collVerified := 0
		collErrors := 0

		// Handle different storage approaches
		if strings.HasSuffix(coll.Path, ".tar") {
			// For TAR files
			collLog.Debugf("Collection is in TAR format, verifying: %s", coll.Path)

			// Open the TAR file
			tarFile, err := os.Open(coll.Path)
			if err != nil {
				collLog.Error(fmt.Errorf("failed to open TAR file: %w", err))
				continue
			}
			defer tarFile.Close()

			// Create TAR reader
			tr := tar.NewReader(tarFile)

			// Process each entry
			for {
				header, err := tr.Next()
				if err == io.EOF {
					break // End of archive
				}
				if err != nil {
					collLog.Error(fmt.Errorf("error reading TAR header: %w", err))
					totalErrors++
					collErrors++
					continue
				}

				// Skip if not a PNG file
				if !strings.HasSuffix(strings.ToUpper(header.Name), ".PNG") {
					continue
				}

				collFiles++
				totalFiles++

				// Get the chunk number for better reporting
				chunkNum := "?"
				parts := strings.Split(strings.TrimSuffix(header.Name, ".PNG"), "_")
				if len(parts) >= 2 {
					chunkNum = parts[1]
				}

				// Read PNG data
				var buf bytes.Buffer
				if _, err := io.Copy(&buf, tr); err != nil {
					collLog.Error(fmt.Errorf("failed to read PNG data from TAR (chunk %s): %w", chunkNum, err))
					totalErrors++
					collErrors++
					continue
				}

				// Try to extract data which verifies CRC
				_, err = file.ExtractDataFromPNG(&buf)
				if err != nil {
					collLog.Error(fmt.Errorf("PNG verification failed for chunk %s: %w", chunkNum, err))
					totalErrors++
					collErrors++
					continue
				}

				// Count successful verification
				collVerified++
				totalVerified++

				// Progress indicator (using dots for conciseness)
				if collVerified%20 == 0 {
					dotPrinted = true
					fmt.Printf(".")
				}
			}

		} else {
			// For directory-based collections
			collLog.Debugf("Collection is directory-based, verifying: %s", coll.Path)

			// Find all PNG files
			pngPattern := filepath.Join(coll.Path, "IMG*.PNG")
			pngFiles, err := filepath.Glob(pngPattern)
			if err != nil {
				collLog.Error(fmt.Errorf("failed to find PNG files: %w", err))
				continue
			}

			collFiles = len(pngFiles)
			totalFiles += collFiles

			// Check each file
			for _, filePath := range pngFiles {
				// Get filename for reporting
				fileName := filepath.Base(filePath)

				// Open the file
				f, err := os.Open(filePath)
				if err != nil {
					collLog.Error(fmt.Errorf("failed to open PNG file %s: %w", fileName, err))
					totalErrors++
					collErrors++
					continue
				}

				// Read the file into memory
				fileData, err := io.ReadAll(f)
				f.Close() // Close immediately after reading

				if err != nil {
					collLog.Error(fmt.Errorf("failed to read PNG file %s: %w", fileName, err))
					totalErrors++
					collErrors++
					continue
				}

				// Try to extract data which verifies CRC
				buf := bytes.NewBuffer(fileData)
				_, err = file.ExtractDataFromPNG(buf)

				if err != nil {
					collLog.Error(fmt.Errorf("PNG verification failed for %s: %w", fileName, err))
					totalErrors++
					collErrors++
					continue
				}

				// Count successful verification
				collVerified++
				totalVerified++

				// Progress indicator (using dots for conciseness)
				if collVerified%20 == 0 {
					dotPrinted = true
					fmt.Printf(".")
				}

			}
		}

		// Report collection results
		if dotPrinted {
			fmt.Printf("\n") // Newline after progress dots
		}
		if collErrors > 0 {
			collLog.Infof("Verified %d/%d files - found %d errors", collVerified, collFiles, collErrors)
		} else if collVerified > 0 {
			collLog.Infof("All %d files verified successfully", collVerified)
		} else {
			collLog.Infof("No files found to verify")
		}
	}

	// Report overall results
	if totalErrors > 0 {
		log.Infof("Verification complete: %d/%d files verified, %d errors detected", totalVerified, totalFiles, totalErrors)
		return fmt.Errorf("PNG verification found %d integrity errors in %d files", totalErrors, totalFiles)
	} else if totalVerified > 0 {
		log.Infof("Verification complete: All %d files passed integrity checks", totalVerified)
		return nil
	} else {
		log.Infof("Verification complete: No files were found to verify")
		return nil
	}
}

// getTimeoutDuration returns an appropriate timeout duration based on the execution environment
// In test environments, it returns a shorter timeout (3 seconds)
// In production environments, it returns a longer timeout (30 seconds)
func getTimeoutDuration(ctx context.Context) time.Duration {
	// Default timeout for production environments
	timeoutDuration := 30 * time.Second
	
	// Check if we're in a test environment
	isTestEnv := os.Getenv("GO_TEST") != ""
	
	// Also check if the context contains a tracer with a TEST prefix
	if !isTestEnv && ctx.Value(trace.TracerKey{}) != nil {
		tracer, ok := ctx.Value(trace.TracerKey{}).(*trace.Tracer)
		if ok && tracer != nil {
			isTestEnv = strings.Contains(tracer.GetPrefix(), "TEST")
		}
	}
	
	// Use shorter timeout for test environments
	if isTestEnv {
		timeoutDuration = 3 * time.Second
	}
	
	return timeoutDuration
}
