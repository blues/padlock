// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

// Package file provides the file system operations for the padlock cryptographic system.
//
// This package handles all interactions with the file system, including:
// - Managing collections (creating, finding, zipping)
// - Serializing and deserializing directories to/from streams
// - Compressing and decompressing data
// - Reading and writing data chunks in different formats
//
// It abstracts the underlying storage details away from the core cryptographic
// functionality in the pad package, allowing the system to work with different
// storage formats and approaches without changing the cryptographic logic.
package file

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blues/padlock/pkg/trace"
)

// Collection represents a collection of encoded data in the padlock system.
//
// A collection is one of the N shares in the K-of-N threshold scheme. Each collection
// contains chunks of encoded data that, when combined with chunks from K-1 other
// collections, can reconstruct the original data. Collections can be stored as
// directories on disk or packaged as ZIP files for distribution.
type Collection struct {
	Name   string // The name of the collection (e.g., "3A5")
	Path   string // The filesystem path to the collection
	Format Format // The format of the data chunks (binary or PNG)
}

// CreateCollections creates collection directories for the padlock scheme
func CreateCollections(ctx context.Context, outputDir string, collectionNames []string) ([]Collection, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION")

	log.Debugf("Creating %d collections in %s", len(collectionNames), outputDir)

	// Create collections
	collections := make([]Collection, len(collectionNames))
	for i, collName := range collectionNames {
		collPath, err := CreateCollectionDirectory(ctx, outputDir, collName)
		if err != nil {
			return nil, err
		}

		collections[i] = Collection{
			Name: collName,
			Path: collPath,
		}

		log.Debugf("Created collection %d: %s at %s", i+1, collName, collPath)
	}

	return collections, nil
}

// FindCollections locates collection directories or TAR files in the input directory
// It handles direct access to TAR files for collections
func FindCollections(ctx context.Context, inputDir string) ([]Collection, string, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION")

	log.Debugf("Finding collections in %s", inputDir)

	// Check if we have files in the input directory
	files, err := os.ReadDir(inputDir)
	if err != nil {
		log.Error(fmt.Errorf("failed to read input directory: %w", err))
		return nil, "", fmt.Errorf("failed to read input directory: %w", err)
	}

	// Gather collections from directories and tar files
	var collections []Collection
	directTarCollections := make(map[string]bool) // Used to track TAR files processed directly
	var tempDir string                            // Temporary directory for TAR extraction

	// First, gather all collection directories
	log.Debugf("Checking for collection directories")
	for _, entry := range files {
		if entry.IsDir() {
			collName := entry.Name()
			// Check if this looks like a collection directory (e.g. "3A5")
			if len(collName) >= 3 && IsCollectionName(collName) {
				collPath := filepath.Join(inputDir, collName)
				log.Debugf("Found collection directory: %s", collPath)

				// Determine the format by looking at the files
				format, err := DetermineCollectionFormat(collPath)
				if err != nil {
					log.Error(fmt.Errorf("failed to determine format for collection %s: %w", collName, err))
					continue
				}

				collections = append(collections, Collection{
					Name:   collName,
					Path:   collPath,
					Format: format,
				})

				log.Debugf("Added collection %s with format %s", collName, format)
			}
		}
	}

	// Process TAR files directly without extraction
	log.Debugf("Checking for collection tar files for direct access")
	for _, entry := range files {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tar") {
			tarPath := filepath.Join(inputDir, entry.Name())
			log.Debugf("Found collection tar file: %s", tarPath)

			// Try to determine collection name from the TAR filename
			// TAR files are usually named after the collection, like "3A5.tar"
			baseName := strings.TrimSuffix(entry.Name(), ".tar")

			// Check if it looks like a valid collection name
			if IsCollectionName(baseName) {
				log.Debugf("Using direct TAR access for collection %s", baseName)

				// Try to open the TAR file to check for contents
				file, err := os.Open(tarPath)
				if err != nil {
					log.Error(fmt.Errorf("failed to open tar file %s: %w", tarPath, err))
					continue
				}

				// Create tar reader directly without gzip decompression
				tarReader := tar.NewReader(file)

				// Determine format by examining TAR entries
				format := Format("")
				for {
					header, err := tarReader.Next()
					if err == io.EOF {
						break // End of archive
					}
					if err != nil {
						log.Error(fmt.Errorf("error reading tar header: %w", err))
						break
					}

					// Check file extension to determine format
					name := header.Name
					if strings.HasSuffix(strings.ToUpper(name), ".PNG") {
						format = FormatPNG
						break
					} else if strings.HasSuffix(name, ".bin") {
						format = FormatBin
						break
					}
				}

				// Close reader
				file.Close()

				if format == "" {
					log.Error(fmt.Errorf("could not determine format for tar file %s", tarPath))
					continue
				}

				// Add the collection for direct access
				collections = append(collections, Collection{
					Name:   baseName,
					Path:   tarPath,
					Format: format,
				})

				directTarCollections[tarPath] = true
				log.Debugf("Added TAR-based collection %s with format %s for direct access", baseName, format)
			} else {
				log.Debugf("TAR filename doesn't match collection name pattern: %s", entry.Name())
				// For TARs without collection names in their filename, we'd need a way to examine
				// their contents to determine collection name. For now, handle them the traditional way.

				// Create a temporary directory for extraction if needed
				if tempDir == "" {
					var err error
					tempDir, err = os.MkdirTemp("", "padlock-collections-")
					if err != nil {
						log.Error(fmt.Errorf("failed to create temp directory: %w", err))
						continue
					}
					log.Debugf("Created temporary directory for TAR extraction: %s", tempDir)
				}

				// Extract the TAR file to the temporary directory
				extractedDir, err := ExtractTarCollection(ctx, tarPath, tempDir)
				if err != nil {
					log.Error(fmt.Errorf("failed to extract TAR file %s: %w", tarPath, err))
					continue
				}

				// Try to determine the collection name from the extracted directory
				collName := filepath.Base(extractedDir)
				if !IsCollectionName(collName) {
					// If the directory name doesn't look like a collection name,
					// try to determine it from file contents
					collName, err = determineCollectionNameFromContent(ctx, extractedDir)
					if err != nil {
						log.Error(fmt.Errorf("failed to determine collection name for extracted TAR: %w", err))
						continue
					}
				}

				// Determine the format
				format, err := DetermineCollectionFormat(extractedDir)
				if err != nil {
					log.Error(fmt.Errorf("failed to determine format for extracted TAR: %w", err))
					continue
				}

				// Add the extracted collection
				collections = append(collections, Collection{
					Name:   collName,
					Path:   extractedDir,
					Format: format,
				})

				log.Debugf("Added extracted TAR collection %s with format %s", collName, format)
			}
		}
	}

	// Check if we found any collections
	if len(collections) == 0 {
		log.Error(fmt.Errorf("no collections found in %s", inputDir))
		if tempDir != "" {
			os.RemoveAll(tempDir)
		}
		return nil, "", fmt.Errorf("no collections found in %s", inputDir)
	}

	// Sort collections by name
	sort.Slice(collections, func(i, j int) bool {
		return collections[i].Name < collections[j].Name
	})

	log.Debugf("Found %d collections", len(collections))
	return collections, tempDir, nil
}

// ZipCollections is a compatibility function that calls TarCollections
// This exists for backward compatibility with any code that might call ZipCollections
func ZipCollections(ctx context.Context, collections []Collection) ([]string, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION")
	log.Infof("ZipCollections function is deprecated, using TarCollections instead")
	return TarCollections(ctx, collections)
}

// DetermineCollectionFormat determines the format of a collection by looking at its files
// Exported so it can be used by other packages
func DetermineCollectionFormat(collPath string) (Format, error) {
	files, err := os.ReadDir(collPath)
	if err != nil {
		return "", fmt.Errorf("failed to read collection directory: %w", err)
	}

	for _, f := range files {
		name := f.Name()
		if !f.IsDir() {
			if strings.HasPrefix(name, "IMG") && strings.HasSuffix(strings.ToUpper(name), ".PNG") {
				return FormatPNG, nil
			} else if strings.HasSuffix(name, ".bin") {
				return FormatBin, nil
			}
		}
	}

	return "", fmt.Errorf("unable to determine format for collection")
}

// IsCollectionName checks if a string looks like a collection name (e.g. "3A5" or "12Z26")
// Exported so it can be used by other packages
func IsCollectionName(name string) bool {
	if len(name) < 3 {
		return false
	}

	// Check if the first character(s) are digits (K)
	firstDigitIndex := -1
	for i := 0; i < len(name); i++ {
		if name[i] >= '0' && name[i] <= '9' {
			firstDigitIndex = i
		} else {
			break
		}
	}

	if firstDigitIndex < 0 {
		return false // Must start with at least one digit
	}

	// After the initial digits, there must be a letter
	if firstDigitIndex+1 >= len(name) {
		return false // No room for letter after digits
	}

	letterChar := name[firstDigitIndex+1]
	if (letterChar < 'A' || letterChar > 'Z') && (letterChar < 'a' || letterChar > 'z') {
		return false // Middle character must be a letter
	}

	// Final position(s) must be digits
	for i := firstDigitIndex + 2; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}

	return true
}

// determineCollectionNameFromContent tries to deduce the collection name by examining files
func determineCollectionNameFromContent(ctx context.Context, dirPath string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION")

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
			if len(parts) > 0 && IsCollectionName(parts[0]) {
				log.Debugf("Determined collection name '%s' from file %s", parts[0], name)
				return parts[0], nil
			}
		}

		// Check for bin files
		if strings.HasSuffix(name, ".bin") {
			// Extract the collection name before "_"
			parts := strings.Split(name, "_")
			if len(parts) > 0 && IsCollectionName(parts[0]) {
				log.Debugf("Determined collection name '%s' from file %s", parts[0], name)
				return parts[0], nil
			}
		}
	}

	return "", fmt.Errorf("could not determine collection name from directory content")
}

// CollectionReader reads data from a collection
type CollectionReader struct {
	Collection       Collection
	ChunkIndex       int
	Formatter        Formatter
	sortedChunkFiles []string    // Cached list of sorted chunk files in directory
	tarFile          *os.File    // File handle for TAR files
	tarReader        *tar.Reader // TAR reader for streaming chunks
}

// NewCollectionReader creates a new collection reader
func NewCollectionReader(collection Collection) *CollectionReader {
	return &CollectionReader{
		Collection: collection,
		ChunkIndex: 1, // Start at chunk 1
		Formatter:  GetFormatter(collection.Format),
	}
}

// ReadNextChunk reads the next chunk from the collection
func (cr *CollectionReader) ReadNextChunk(ctx context.Context) ([]byte, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION-READER")

	log.Debugf("Reading next chunk %d from collection %s (path: %s)",
		cr.ChunkIndex, cr.Collection.Name, cr.Collection.Path)

	// Check if this collection is a TAR file
	if strings.HasSuffix(cr.Collection.Path, ".tar") {
		log.Debugf("Collection is a TAR file, using TAR reader")
		// Read directly from TAR file
		return cr.readNextChunkFromTar(ctx)
	}

	// Lazy initialization of sorted chunk files list for directory-based collections
	if cr.sortedChunkFiles == nil {
		log.Debugf("Initializing sorted chunk files for collection in directory %s", cr.Collection.Path)

		// Read all files in the directory
		entries, err := os.ReadDir(cr.Collection.Path)
		if err != nil {
			log.Error(fmt.Errorf("failed to read collection directory: %w", err))
			return nil, fmt.Errorf("failed to read collection directory: %w", err)
		}

		// Filter for chunk files based on extension
		var chunkFiles []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}

			name := entry.Name()
			ext := strings.ToUpper(filepath.Ext(name))

			// Check if it's a valid chunk file based on extension
			if (cr.Collection.Format == FormatPNG && (ext == ".PNG" || ext == ".png")) ||
				(cr.Collection.Format == FormatBin && ext == ".bin") ||
				(cr.Collection.Format == "" && (ext == ".PNG" || ext == ".png" || ext == ".bin")) {
				chunkFiles = append(chunkFiles, name)
			}
		}

		// If no chunk files found, return EOF
		if len(chunkFiles) == 0 {
			log.Debugf("No chunk files found in collection directory: %s", cr.Collection.Path)
			return nil, io.EOF
		}

		// Sort the chunk files to ensure consistent ordering
		sort.Strings(chunkFiles)

		// Log the sorted files for debugging
		if len(chunkFiles) > 0 {
			log.Debugf("Sorted %d chunk files, first: %s, last: %s",
				len(chunkFiles), chunkFiles[0], chunkFiles[len(chunkFiles)-1])
		}

		// Store the sorted chunk files
		cr.sortedChunkFiles = chunkFiles
		log.Debugf("Found and sorted %d chunk files in directory", len(chunkFiles))
	}

	// Check if we've reached the end of the chunk files
	if cr.ChunkIndex > len(cr.sortedChunkFiles) {
		log.Debugf("No more chunks in collection (reached end of sorted files)")
		return nil, io.EOF
	}

	// Get the current chunk file
	chunkFile := cr.sortedChunkFiles[cr.ChunkIndex-1]
	filePath := filepath.Join(cr.Collection.Path, chunkFile)

	log.Debugf("Reading chunk %d (file: %s) from collection %s", cr.ChunkIndex, chunkFile, cr.Collection.Name)

	// Read the chunk data
	var data []byte
	var err error

	// Use the appropriate method to read the data based on file extension
	ext := strings.ToUpper(filepath.Ext(chunkFile))
	if ext == ".PNG" || ext == ".png" {
		// Use PNG format to read the file
		f, err := os.Open(filePath)
		if err != nil {
			log.Error(fmt.Errorf("failed to open PNG file: %w", err))
			return nil, fmt.Errorf("failed to open chunk file: %w", err)
		}
		defer f.Close()

		data, err = ExtractDataFromPNG(f)
		if err != nil {
			log.Error(fmt.Errorf("failed to extract data from PNG: %w", err))
			return nil, fmt.Errorf("failed to extract data from PNG: %w", err)
		}
	} else {
		// Default to binary format
		data, err = os.ReadFile(filePath)
		if err != nil {
			log.Error(fmt.Errorf("failed to read chunk file: %w", err))
			return nil, fmt.Errorf("failed to read chunk file: %w", err)
		}
	}

	log.Debugf("Successfully read %d bytes from chunk file %s", len(data), chunkFile)

	// Increment the chunk index for the next read
	cr.ChunkIndex++

	return data, nil
}

// readNextChunkFromTar reads the next chunk directly from a TAR file
func (cr *CollectionReader) readNextChunkFromTar(ctx context.Context) ([]byte, error) {
	log := trace.FromContext(ctx).WithPrefix("TAR-READER")

	// If this is the first time accessing the TAR file, open it and prepare the reader
	if cr.tarFile == nil {
		log.Debugf("Opening TAR file for streaming: %s", cr.Collection.Path)

		// Open the TAR file
		file, err := os.Open(cr.Collection.Path)
		if err != nil {
			log.Error(fmt.Errorf("failed to open TAR file: %w", err))
			return nil, fmt.Errorf("failed to open TAR file: %w", err)
		}

		// Store the file handle so we can close it later
		cr.tarFile = file

		// Create tar reader directly without gzip decompression
		cr.tarReader = tar.NewReader(file)

		log.Debugf("Set up TAR streaming for collection %s", cr.Collection.Name)
	}

	// Read and process the next entry from the TAR file
	for {
		header, err := cr.tarReader.Next()
		if err == io.EOF {
			log.Debugf("Reached end of TAR file %s", cr.Collection.Path)
			// Close the file when we reach the end
			if cr.tarFile != nil {
				cr.tarFile.Close()
				cr.tarFile = nil
			}
			return nil, io.EOF
		}
		if err != nil {
			log.Error(fmt.Errorf("error reading TAR header: %w", err))
			// Close on error
			if cr.tarFile != nil {
				cr.tarFile.Close()
				cr.tarFile = nil
			}
			return nil, fmt.Errorf("error reading TAR header: %w", err)
		}

		// Get the file name and extension
		name := header.Name
		ext := strings.ToUpper(filepath.Ext(name))

		// Check if it's a valid chunk file based on extension
		if (cr.Collection.Format == FormatPNG && (ext == ".PNG" || ext == ".png")) ||
			(cr.Collection.Format == FormatBin && ext == ".bin") ||
			(cr.Collection.Format == "" && (ext == ".PNG" || ext == ".png" || ext == ".bin")) {

			log.Debugf("Reading chunk %d (file: %s) from TAR stream for collection %s",
				cr.ChunkIndex, name, cr.Collection.Name)

			// Read the chunk content
			var data []byte
			var err error

			if ext == ".PNG" || ext == ".png" {
				// For PNG files, extract data from the PNG
				var buf bytes.Buffer
				bytesRead, err := io.Copy(&buf, cr.tarReader)
				if err != nil {
					log.Error(fmt.Errorf("failed to read PNG from TAR (read %d bytes): %w", bytesRead, err))
					continue
				}

				log.Debugf("Successfully read %d bytes from TAR chunk %s", bytesRead, name)

				// Extract data from the PNG with enhanced error reporting
				data, err = ExtractDataFromPNG(&buf)
				if err != nil {
					// Detailed error logging for PNG extraction failure
					pngErr := fmt.Errorf("failed to extract data from PNG in TAR: %w", err)
					log.Error(pngErr)

					// Save a copy of the problematic PNG for debugging if needed
					if buf.Len() > 0 {
						log.Debugf("PNG error analysis: PNG size=%d bytes, first 16 bytes: %x",
							buf.Len(),
							buf.Bytes()[:min(16, buf.Len())])
					}

					// Return the error rather than just continuing, to help with debugging
					return nil, pngErr
				}
			} else {
				// For binary files, just read the content
				data, err = io.ReadAll(cr.tarReader)
				if err != nil {
					log.Error(fmt.Errorf("failed to read binary data from TAR: %w", err))
					continue
				}
			}

			log.Debugf("Successfully read %d bytes from TAR chunk %s", len(data), name)

			// Increment the chunk index for the next read
			cr.ChunkIndex++

			return data, nil
		} else {
			// Skip this entry but consume its content
			log.Debugf("Skipping non-chunk file in TAR: %s", name)
			_, err = io.Copy(io.Discard, cr.tarReader)
			if err != nil {
				log.Error(fmt.Errorf("error skipping TAR entry: %w", err))
			}
		}
	}
}

// min is a helper function to get the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
