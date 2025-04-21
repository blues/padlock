// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

// Package file provides utilities for file and directory management in the padlock system.
//
// This package implements various file handling operations critical to the padlock
// threshold one-time-pad cryptographic system, including:
// - Format-specific chunk handling (binary, PNG)
// - Collection management and naming conventions
// - ZIP archive support for distribution and backup
// - Directory validation and management
// - Serialization of chunk data
//
// Key components:
// - Formatters: Handlers for different storage formats (binary, PNG)
// - Collection management: Operations for creating, reading, and managing collections
// - File naming conventions: Implementation of the padlock naming scheme
// - Directory utilities: Path validation and directory operations
// - Error handling: Consistent approach to file I/O errors
//
// Usage notes:
// - The system uses a consistent file naming convention: "<collectionName>_<chunkNumber>.<format>"
// - PNG format provides steganographic capabilities (hiding data in image files)
// - Binary format offers maximum efficiency and minimal overhead
// - All operations provide detailed logging through the context's trace facility
package file

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/blues/padlock/pkg/trace"
)

// Format represents the output format used by padlock for storing encoded chunks.
// The choice of format affects visibility, storage efficiency, and distribution options.
type Format string

const (
	// FormatBin represents a raw binary format for maximum efficiency.
	// This format stores chunk data directly as binary files with minimal overhead,
	// making it suitable for internal or back-end storage where stealth is not required.
	FormatBin Format = "bin"

	// FormatPNG represents the PNG image format for steganographic storage.
	// This format embeds chunk data within PNG image files, making the data
	// appear as ordinary images to casual observers. It provides a level of
	// stealth at the cost of some storage efficiency.
	// The encoded chunks are stored in a custom PNG chunk type 'rAWd'.
	FormatPNG Format = "png"
)

// Formatter defines the interface for different chunk storage formats.
//
// This interface abstracts the specific storage format implementation details,
// allowing the pad system to work with different storage formats interchangeably.
// Implementations handle the specifics of storing and retrieving chunk data
// in their respective formats, including file naming conventions and any
// format-specific encoding/decoding.
//
// Current implementations include:
// - BinFormatter: Raw binary storage for maximum efficiency
// - PngFormatter: PNG image storage for steganographic purposes
//
// The system can be extended with new formatters as needed for specialized storage.
type Formatter interface {
	// WriteChunk writes a chunk of data to a file in the specified collection.
	//
	// Parameters:
	//   - ctx: Context for logging, cancellation, and tracing
	//   - collectionPath: Path to the collection directory
	//   - collectionIndex: Index of the collection (0-based)
	//   - chunkNumber: The sequential number of this chunk (1-based)
	//   - data: The chunk data to be written
	//
	// Returns an error if the write operation fails.
	WriteChunk(ctx context.Context, collectionPath string, collectionIndex int, chunkNumber int, data []byte) error

	// ReadChunk reads a chunk of data from a file in the specified collection.
	//
	// Parameters:
	//   - ctx: Context for logging, cancellation, and tracing
	//   - collectionPath: Path to the collection directory
	//   - collectionIndex: Index of the collection (0-based)
	//   - chunkNumber: The sequential number of this chunk (1-based)
	//
	// Returns:
	//   - The chunk data as a byte slice
	//   - An error if the read operation fails or the chunk does not exist
	ReadChunk(ctx context.Context, collectionPath string, collectionIndex int, chunkNumber int) ([]byte, error)
}

// BinFormatter implements the Formatter interface for binary file storage.
//
// This formatter stores chunk data directly as binary files with minimal overhead,
// making it suitable for internal or backend storage where efficiency is prioritized
// over stealth. Binary storage provides:
// - Maximum storage efficiency (no format overhead)
// - Direct access to raw data
// - Simplicity in implementation and debugging
// - Faster processing compared to more complex formats
//
// File naming convention: "<collectionName>_<chunkNumber>.bin"
// Example: "3A5_0001.bin"
type BinFormatter struct{}

// WriteChunk writes a chunk to a binary file
func (bf *BinFormatter) WriteChunk(ctx context.Context, collectionPath string, collectionIndex int, chunkNumber int, data []byte) error {
	log := trace.FromContext(ctx).WithPrefix("BIN-FORMATTER")

	base := filepath.Base(collectionPath)
	fname := fmt.Sprintf("%s_%04d.bin", base, chunkNumber)
	fp := filepath.Join(collectionPath, fname)

	log.Debugf("Writing chunk %d to binary file: %s", chunkNumber, fp)

	if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
		log.Error(fmt.Errorf("failed to create chunk directory: %w", err))
		return fmt.Errorf("failed to create chunk directory: %w", err)
	}

	f, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Error(fmt.Errorf("failed to open chunk file: %w", err))
		return fmt.Errorf("failed to open chunk file: %w", err)
	}
	defer f.Close()

	if _, werr := f.Write(data); werr != nil {
		log.Error(fmt.Errorf("failed to write chunk data: %w", werr))
		return fmt.Errorf("failed to write chunk data: %w", werr)
	}

	if err := f.Sync(); err != nil {
		log.Error(fmt.Errorf("failed to sync chunk file: %w", err))
		return fmt.Errorf("failed to sync chunk file: %w", err)
	}

	log.Debugf("Successfully wrote %d bytes to chunk file", len(data))
	return nil
}

// ReadChunk reads a chunk from a binary file
func (bf *BinFormatter) ReadChunk(ctx context.Context, collectionPath string, collectionIndex int, chunkNumber int) ([]byte, error) {
	log := trace.FromContext(ctx).WithPrefix("BIN-FORMATTER")

	base := filepath.Base(collectionPath)
	fname := fmt.Sprintf("%s_%04d.bin", base, chunkNumber)
	fp := filepath.Join(collectionPath, fname)

	log.Debugf("Reading chunk %d from binary file: %s", chunkNumber, fp)

	// Check if the file exists
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		log.Debugf("Chunk file does not exist: %s", fp)
		// Return a more informative error message
		return nil, fmt.Errorf("chunk file does not exist: %s", fp)
	}

	data, err := os.ReadFile(fp)
	if err != nil {
		log.Error(fmt.Errorf("failed to read chunk file: %w", err))
		return nil, fmt.Errorf("failed to read chunk file: %w", err)
	}

	log.Debugf("Successfully read %d bytes from chunk file", len(data))
	return data, nil
}

// PngFormatter implements the Formatter interface for PNG image storage.
//
// This formatter embeds chunk data within PNG image files using a custom
// chunk type ('rAWd'), providing steganographic capabilities. This allows
// the data to appear as ordinary images to casual observers, offering:
// - Stealth storage (data appears as normal PNG images)
// - Plausible deniability
// - Compatibility with standard image viewers and tools
// - Ability to blend into normal file systems
//
// Security considerations:
// - While providing visual obfuscation, this is NOT cryptographic protection
// - The custom chunk type ('rAWd') could be detected by specialized tools
// - Additional storage overhead compared to raw binary format
//
// File naming convention: "IMG<collectionName>_<chunkNumber>.PNG"
// Example: "IMG3A5_0001.PNG"
type PngFormatter struct{}

// WriteChunk writes a chunk to a PNG file
func (pf *PngFormatter) WriteChunk(ctx context.Context, collectionPath string, collectionIndex int, chunkNumber int, data []byte) error {
	log := trace.FromContext(ctx).WithPrefix("PNG-FORMATTER")

	base := filepath.Base(collectionPath)
	fname := fmt.Sprintf("IMG%s_%04d.PNG", base, chunkNumber)
	fp := filepath.Join(collectionPath, fname)

	log.Debugf("Writing chunk %d to PNG file: %s", chunkNumber, fp)

	if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
		log.Error(fmt.Errorf("failed to create chunk directory: %w", err))
		return fmt.Errorf("failed to create chunk directory: %w", err)
	}

	f, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Error(fmt.Errorf("failed to open PNG file %s: %w", fp, err))
		return fmt.Errorf("failed to open PNG file %s: %w", fp, err)
	}
	defer f.Close()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.Transparent)
	if err := encodePNGWithData(f, img, data); err != nil {
		f.Close()
		os.Remove(fp)
		log.Error(fmt.Errorf("failed to encode PNG with data for %s: %w", fp, err))
		return fmt.Errorf("failed to encode PNG with data for %s: %w", fp, err)
	}

	if err := f.Sync(); err != nil {
		log.Error(fmt.Errorf("failed to sync PNG file: %w", err))
		return fmt.Errorf("failed to sync PNG file: %w", err)
	}

	log.Debugf("Successfully wrote %d bytes to PNG file", len(data))
	return nil
}

// ReadChunk reads a chunk from a PNG file
func (pf *PngFormatter) ReadChunk(ctx context.Context, collectionPath string, collectionIndex int, chunkNumber int) ([]byte, error) {
	log := trace.FromContext(ctx).WithPrefix("PNG-FORMATTER")

	base := filepath.Base(collectionPath)
	fname := fmt.Sprintf("IMG%s_%04d.PNG", base, chunkNumber)
	fp := filepath.Join(collectionPath, fname)

	log.Debugf("Reading chunk %d from PNG file: %s", chunkNumber, fp)

	// Check if the file exists
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		log.Debugf("Chunk file does not exist: %s", fp)
		// Return a more informative error message
		return nil, fmt.Errorf("chunk file does not exist: %s", fp)
	}

	f, err := os.Open(fp)
	if err != nil {
		log.Error(fmt.Errorf("failed to open PNG file: %w", err))
		return nil, fmt.Errorf("failed to open PNG file: %w", err)
	}
	defer f.Close()

	data, err := ExtractDataFromPNG(f)
	if err != nil {
		log.Error(fmt.Errorf("failed to extract data from PNG: %w", err))
		return nil, fmt.Errorf("failed to extract data from PNG: %w", err)
	}

	log.Debugf("Successfully read %d bytes from PNG file", len(data))
	return data, nil
}

// GetFormatter returns a Formatter for the specified format
func GetFormatter(format Format) Formatter {
	switch format {
	case FormatPNG:
		return &PngFormatter{}
	case FormatBin:
		return &BinFormatter{}
	default:
		return &BinFormatter{} // Default to binary format
	}
}

// encodePNGWithData injects data into a custom 'rAWd' chunk in a PNG image.
//
// This function implements PNG steganography by creating a custom chunk type
// that isn't recognized by standard PNG viewers but is preserved during normal
// image operations. It works by:
// 1. Creating a minimal PNG image (typically 1x1 pixel transparent)
// 2. Inserting a custom 'rAWd' chunk with the payload data
// 3. Ensuring proper CRC calculation and chunk structure
// 4. Maintaining valid PNG format for compatibility with standard tools
//
// Parameters:
//   - w: The output writer to receive the encoded PNG
//   - img: A minimal image to serve as the visible part of the PNG
//   - data: The chunk data to embed in the PNG
//
// Security notes:
//   - The 'rAWd' chunk type follows PNG specifications but is non-standard
//   - The data is NOT encrypted by this function (encryption happens earlier)
//   - Specialized PNG analysis tools could detect the presence of custom chunks
func encodePNGWithData(w io.Writer, img image.Image, data []byte) error {
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.DefaultCompression}).Encode(&buf, img); err != nil {
		return fmt.Errorf("PNG encode error: %w", err)
	}
	pngBytes := buf.Bytes()

	if len(pngBytes) < 12 {
		return fmt.Errorf("invalid PNG (too short)")
	}
	iendPos := bytes.Index(pngBytes, []byte("IEND"))
	if iendPos == -1 || iendPos < 4 {
		return fmt.Errorf("invalid PNG, IEND not found")
	}
	iendPos -= 4

	if _, err := w.Write(pngBytes[:iendPos]); err != nil {
		return fmt.Errorf("writing PNG prefix: %w", err)
	}

	chunkType := []byte("rAWd")
	length := uint32(len(data))
	var lengthBytes [4]byte
	binary.BigEndian.PutUint32(lengthBytes[:], length)
	if _, err := w.Write(lengthBytes[:]); err != nil {
		return fmt.Errorf("writing chunk length: %w", err)
	}
	if _, err := w.Write(chunkType); err != nil {
		return fmt.Errorf("writing chunk type: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing chunk data: %w", err)
	}
	crc := crc32.NewIEEE()
	crc.Write(chunkType)
	crc.Write(data)
	var crcBytes [4]byte
	binary.BigEndian.PutUint32(crcBytes[:], crc.Sum32())
	if _, err := w.Write(crcBytes[:]); err != nil {
		return fmt.Errorf("writing chunk CRC: %w", err)
	}

	if _, err := w.Write(pngBytes[iendPos:]); err != nil {
		return fmt.Errorf("writing IEND: %w", err)
	}
	return nil
}

// ExtractDataFromPNG extracts embedded data from a PNG's custom 'rAWd' chunk.
//
// This function reverses the steganographic encoding performed by encodePNGWithData,
// recovering the original data embedded in the custom chunk. The process is:
// 1. Read the entire PNG file into memory
// 2. Locate the 'rAWd' custom chunk
// 3. Extract the data payload from the chunk
// 4. Verify the CRC to ensure data integrity
//
// Parameters:
//   - r: Reader providing the PNG data to extract from
//
// Returns:
//   - The extracted data as a byte slice
//   - An error if the operation fails (invalid PNG, missing chunk, CRC error)
//
// Security notes:
//   - CRC verification ensures data hasn't been corrupted or tampered with
//   - No decryption is performed (that happens later in the pad decoding process)
//   - Fails gracefully if the PNG doesn't contain the expected chunk
func ExtractDataFromPNG(r io.Reader) ([]byte, error) {
	all, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read PNG data: %w", err)
	}
	chunkType := []byte("rAWd")
	chunkPos := bytes.Index(all, chunkType)
	if chunkPos == -1 {
		return nil, fmt.Errorf("'rAWd' chunk not found")
	}
	if chunkPos < 4 {
		return nil, fmt.Errorf("invalid structure, chunk at offset <4")
	}
	lengthBuf := all[chunkPos-4 : chunkPos]
	length := binary.BigEndian.Uint32(lengthBuf)
	dataStart := chunkPos + len(chunkType)
	dataEnd := dataStart + int(length)
	if dataEnd > len(all) {
		return nil, fmt.Errorf("invalid PNG chunk length, out of range")
	}
	extracted := all[dataStart:dataEnd]
	crcPos := dataEnd
	if crcPos+4 > len(all) {
		return nil, fmt.Errorf("invalid chunk: no CRC found")
	}
	expectedCRC := binary.BigEndian.Uint32(all[crcPos : crcPos+4])
	crcCalc := crc32.NewIEEE()
	crcCalc.Write(chunkType)
	crcCalc.Write(extracted)
	if crcCalc.Sum32() != expectedCRC {
		return nil, fmt.Errorf("CRC mismatch in 'rAWd' chunk")
	}
	return extracted, nil
}
