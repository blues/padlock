// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

package file

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blues/padlock/pkg/trace"
)

func TestCreateCollections(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "collection-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Define collection names
	collectionNames := []string{"3A5", "3B5", "3C5"}

	// Create collections
	collections, err := CreateCollections(ctx, tempDir, collectionNames)
	if err != nil {
		t.Fatalf("CreateCollections failed: %v", err)
	}

	// Verify collections were created
	if len(collections) != len(collectionNames) {
		t.Errorf("Expected %d collections, got %d", len(collectionNames), len(collections))
	}

	// Verify collection properties
	for i, coll := range collections {
		if coll.Name != collectionNames[i] {
			t.Errorf("Collection %d: expected name %s, got %s", i, collectionNames[i], coll.Name)
		}

		expectedPath := filepath.Join(tempDir, collectionNames[i])
		if coll.Path != expectedPath {
			t.Errorf("Collection %d: expected path %s, got %s", i, expectedPath, coll.Path)
		}

		// Check if directory exists
		if _, err := os.Stat(coll.Path); os.IsNotExist(err) {
			t.Errorf("Collection directory does not exist: %s", coll.Path)
		}
	}
}

func TestFindCollections(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "find-collections-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create collection directories
	collectionNames := []string{"3A5", "3B5", "3C5"}
	for _, name := range collectionNames {
		collPath := filepath.Join(tempDir, name)
		if err := os.MkdirAll(collPath, 0755); err != nil {
			t.Fatalf("Failed to create collection dir: %v", err)
		}

		// Add a file with the appropriate format extension
		if name == "3A5" {
			binFile := filepath.Join(collPath, "3A5_0001.bin")
			if err := os.WriteFile(binFile, []byte("test"), 0644); err != nil {
				t.Fatalf("Failed to create bin file: %v", err)
			}
		} else if name == "3B5" {
			pngFile := filepath.Join(collPath, "IMG3B5_0001.PNG")
			if err := os.WriteFile(pngFile, []byte("test"), 0644); err != nil {
				t.Fatalf("Failed to create PNG file: %v", err)
			}
		} else {
			// Mix of formats
			binFile := filepath.Join(collPath, "3C5_0001.bin")
			if err := os.WriteFile(binFile, []byte("test"), 0644); err != nil {
				t.Fatalf("Failed to create bin file: %v", err)
			}
		}
	}

	// Test FindCollections
	collections, tempDirCreated, err := FindCollections(ctx, tempDir)
	if err != nil {
		t.Fatalf("FindCollections failed: %v", err)
	}

	// No temporary directory should have been created
	if tempDirCreated != "" {
		t.Errorf("Unexpected temporary directory created: %s", tempDirCreated)
		defer os.RemoveAll(tempDirCreated)
	}

	// Verify collections were found
	if len(collections) != len(collectionNames) {
		t.Errorf("Expected %d collections, got %d", len(collectionNames), len(collections))
	}

	// Verify collection formats were correctly determined
	for _, coll := range collections {
		switch coll.Name {
		case "3A5":
			if coll.Format != FormatBin {
				t.Errorf("Collection 3A5: expected format %s, got %s", FormatBin, coll.Format)
			}
		case "3B5":
			if coll.Format != FormatPNG {
				t.Errorf("Collection 3B5: expected format %s, got %s", FormatPNG, coll.Format)
			}
		case "3C5":
			if coll.Format != FormatBin {
				t.Errorf("Collection 3C5: expected format %s, got %s", FormatBin, coll.Format)
			}
		}
	}
}

func TestZipCollections(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "zip-collections-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test collection
	collName := "3A5"
	collPath := filepath.Join(tempDir, collName)
	if err := os.MkdirAll(collPath, 0755); err != nil {
		t.Fatalf("Failed to create collection dir: %v", err)
	}

	// Add some test files to the collection
	testFiles := []string{"3A5_0001.bin", "3A5_0002.bin"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(collPath, fileName)
		if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Create a collection object
	collections := []Collection{
		{
			Name: collName,
			Path: collPath,
		},
	}

	// Test ZipCollections
	zipPaths, err := ZipCollections(ctx, collections)
	if err != nil {
		t.Fatalf("ZipCollections failed: %v", err)
	}

	// Verify zip file was created
	if len(zipPaths) != 1 {
		t.Errorf("Expected 1 zip path, got %d", len(zipPaths))
	}

	expectedZipPath := collPath + ".zip"
	if zipPaths[0] != expectedZipPath {
		t.Errorf("Expected zip path %s, got %s", expectedZipPath, zipPaths[0])
	}

	// Verify zip file exists
	if _, err := os.Stat(expectedZipPath); os.IsNotExist(err) {
		t.Errorf("Zip file does not exist: %s", expectedZipPath)
	}

	// Verify original directory was removed
	if _, err := os.Stat(collPath); !os.IsNotExist(err) {
		t.Errorf("Original collection directory should have been removed: %s", collPath)
	}
}

func TestCollectionReader(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "collection-reader-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test collection
	collName := "3A5"
	collPath := filepath.Join(tempDir, collName)
	if err := os.MkdirAll(collPath, 0755); err != nil {
		t.Fatalf("Failed to create collection dir: %v", err)
	}

	// Add some test chunk files
	chunkContents := []string{
		"chunk 1 content",
		"chunk 2 content",
		"chunk 3 content",
	}

	for i, content := range chunkContents {
		chunkFile := filepath.Join(collPath, fmt.Sprintf("%s_000%d.bin", collName, i+1))
		if err := os.WriteFile(chunkFile, []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create chunk file: %v", err)
		}
	}

	// Create a collection and reader
	collection := Collection{
		Name:   collName,
		Path:   collPath,
		Format: FormatBin,
	}

	reader := NewCollectionReader(collection)

	// Read chunks and verify content
	for i, expectedContent := range chunkContents {
		data, err := reader.ReadNextChunk(ctx)
		if err != nil {
			t.Fatalf("ReadNextChunk %d failed: %v", i+1, err)
		}

		if string(data) != expectedContent {
			t.Errorf("Chunk %d: expected content %q, got %q", i+1, expectedContent, string(data))
		}
	}

	// Reading past the end should return EOF
	_, err = reader.ReadNextChunk(ctx)
	if err != io.EOF {
		t.Errorf("Expected EOF after reading all chunks, got %v", err)
	}
}

func TestIsCollectionName(t *testing.T) {
	tests := []struct {
		name     string
		collName string
		expect   bool
	}{
		{"Valid 3A5", "3A5", true},
		{"Valid 2B4", "2B4", true},
		{"Valid lowercase", "3a5", true},
		{"Too short", "3A", false},
		{"Invalid first char", "A35", false},
		{"Invalid middle char", "353", false},
		{"Invalid last char", "3AX", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCollectionName(tt.collName)
			if result != tt.expect {
				t.Errorf("isCollectionName(%s) = %v, want %v", tt.collName, result, tt.expect)
			}
		})
	}
}
