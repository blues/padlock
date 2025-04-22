// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

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

// FindCollections locates collection directories or ZIP files in the input directory
func FindCollections(ctx context.Context, inputDir string) ([]Collection, string, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION")

	log.Debugf("Finding collections in %s", inputDir)

	// Create a temporary directory for extracted zip files if needed
	tempDir := ""
	hasZipFiles := false

	// Check if we have zip files in the input directory
	files, err := os.ReadDir(inputDir)
	if err != nil {
		log.Error(fmt.Errorf("failed to read input directory: %w", err))
		return nil, "", fmt.Errorf("failed to read input directory: %w", err)
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".zip" {
			hasZipFiles = true
			break
		}
	}

	if hasZipFiles {
		log.Debugf("Found zip files, creating temporary directory for extraction")
		var err error
		tempDir, err = os.MkdirTemp("", "padlock-*")
		if err != nil {
			log.Error(fmt.Errorf("failed to create temporary directory: %w", err))
			return nil, "", fmt.Errorf("failed to create temporary directory: %w", err)
		}
		log.Debugf("Created temporary directory: %s", tempDir)
	}

	// Gather collections from directories and zip files
	var collections []Collection

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

	// Then extract zip files if needed
	if hasZipFiles {
		log.Debugf("Checking for collection zip files")
		for _, entry := range files {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".zip" {
				zipPath := filepath.Join(inputDir, entry.Name())
				log.Debugf("Found collection zip file: %s", zipPath)

				// Extract the zip file
				extractedDir, err := ExtractZipCollection(ctx, zipPath, tempDir)
				if err != nil {
					log.Error(fmt.Errorf("failed to extract zip collection %s: %w", zipPath, err))
					continue
				}

				collName := filepath.Base(extractedDir)
				if !IsCollectionName(collName) {
					log.Error(fmt.Errorf("invalid collection name in zip file: %s", collName))
					continue
				}

				// Determine the format by looking at the files
				format, err := DetermineCollectionFormat(extractedDir)
				if err != nil {
					log.Error(fmt.Errorf("failed to determine format for extracted collection %s: %w", collName, err))
					continue
				}

				collections = append(collections, Collection{
					Name:   collName,
					Path:   extractedDir,
					Format: format,
				})

				log.Debugf("Added collection %s from zip with format %s", collName, format)
			}
		}
	}

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

// ZipCollections creates zip archives for each collection
func ZipCollections(ctx context.Context, collections []Collection) ([]string, error) {
	log := trace.FromContext(ctx).WithPrefix("COLLECTION")

	log.Infof("Creating zip archives for %d collections", len(collections))
	zipPaths := make([]string, len(collections))

	for i, coll := range collections {
		zipPath, err := ZipCollection(ctx, coll.Path)
		if err != nil {
			log.Error(fmt.Errorf("failed to create zip for collection %s: %w", coll.Name, err))
			return nil, err
		}

		// Remove the original directory
		if err := CleanupCollectionDirectory(ctx, coll.Path); err != nil {
			log.Error(fmt.Errorf("failed to remove original collection directory after zipping: %w", err))
			return nil, err
		}

		zipPaths[i] = zipPath
		log.Infof("Created zip archive for collection %s: %s", coll.Name, zipPath)
	}

	return zipPaths, nil
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

// IsCollectionName checks if a string looks like a collection name (e.g. "3A5")
// Exported so it can be used by other packages
func IsCollectionName(name string) bool {
	if len(name) < 3 {
		return false
	}

	// Check if the first character is a digit (K)
	if name[0] < '0' || name[0] > '9' {
		return false
	}

	// Check if the middle character is a letter (A-Z)
	middleChar := name[1]
	if (middleChar < 'A' || middleChar > 'Z') && (middleChar < 'a' || middleChar > 'z') {
		return false
	}

	// Check if the last character is a digit (N)
	lastChar := name[len(name)-1]
	if lastChar < '0' || lastChar > '9' {
		return false
	}

	return true
}

// CollectionReader reads data from a collection
type CollectionReader struct {
	Collection      Collection
	ChunkIndex      int
	Formatter       Formatter
	sortedChunkFiles []string // Cached list of sorted chunk files in directory
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
	
	// Lazy initialization of sorted chunk files list
	if cr.sortedChunkFiles == nil {
		log.Debugf("Initializing sorted chunk files for collection in %s", cr.Collection.Path)
		
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
		
		// Sort the chunk files alphabetically
		sort.Strings(chunkFiles)
		
		// Store the sorted chunk files
		cr.sortedChunkFiles = chunkFiles
		log.Debugf("Found and sorted %d chunk files", len(chunkFiles))
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