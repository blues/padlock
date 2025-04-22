// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

package file

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blues/padlock/pkg/trace"
)

func TestCreateCollections(t *testing.T) {
	// Create a temporary output directory
	tempDir, err := os.MkdirTemp("", "padlock-test-*")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a context with tracer
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Define test collection names
	collectionNames := []string{"2A3", "2B3", "2C3"}

	// Call CreateCollections
	collections, err := CreateCollections(ctx, tempDir, collectionNames)
	if err != nil {
		t.Fatalf("CreateCollections failed: %v", err)
	}

	// Verify the number of collections
	if len(collections) != len(collectionNames) {
		t.Errorf("Expected %d collections, got %d", len(collectionNames), len(collections))
	}

	// Verify each collection
	for i, coll := range collections {
		// Verify collection name
		if coll.Name != collectionNames[i] {
			t.Errorf("Collection %d name = %s, want %s", i, coll.Name, collectionNames[i])
		}

		// Verify collection path
		expectedPath := filepath.Join(tempDir, collectionNames[i])
		if coll.Path != expectedPath {
			t.Errorf("Collection %d path = %s, want %s", i, coll.Path, expectedPath)
		}

		// Verify directory exists
		stat, err := os.Stat(coll.Path)
		if err != nil {
			t.Errorf("Failed to stat collection directory %s: %v", coll.Path, err)
		} else if !stat.IsDir() {
			t.Errorf("Collection path %s is not a directory", coll.Path)
		}
	}
}

func TestTarCollections(t *testing.T) {
	// Create a temporary output directory
	tempDir, err := os.MkdirTemp("", "padlock-test-*")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a context with tracer
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create sample collection directories
	collections := []Collection{
		{
			Name: "2A3",
			Path: filepath.Join(tempDir, "2A3"),
		},
		{
			Name: "2B3",
			Path: filepath.Join(tempDir, "2B3"),
		},
	}

	// Create directories
	for _, coll := range collections {
		if err := os.MkdirAll(coll.Path, 0755); err != nil {
			t.Fatalf("Failed to create directory %s: %v", coll.Path, err)
		}

		// Create a sample file in each collection
		sampleFile := filepath.Join(coll.Path, "sample.txt")
		if err := os.WriteFile(sampleFile, []byte("test data"), 0644); err != nil {
			t.Fatalf("Failed to write sample file %s: %v", sampleFile, err)
		}
	}

	// Store original functions before mocking
	origTarCollection := TarCollection
	origCleanupCollectionDirectory := CleanupCollectionDirectory

	// Create test variables for tracking function calls
	tarPaths := make([]string, len(collections))
	tarCalled := make([]bool, len(collections))
	cleanupCalled := make([]bool, len(collections))
	
	// Mock implementations
	TarCollection = func(ctx context.Context, collPath string) (string, error) {
		for i, coll := range collections {
			if collPath == coll.Path {
				tarPaths[i] = collPath + ".tar"
				tarCalled[i] = true
				return tarPaths[i], nil
			}
		}
		return "", nil
	}
	
	CleanupCollectionDirectory = func(ctx context.Context, collPath string) error {
		for i, coll := range collections {
			if collPath == coll.Path {
				cleanupCalled[i] = true
				return nil
			}
		}
		return nil
	}

	// Restore the original functions after the test
	defer func() {
		TarCollection = origTarCollection
		CleanupCollectionDirectory = origCleanupCollectionDirectory
	}()

	// Call TarCollections
	resultPaths, err := TarCollections(ctx, collections)
	if err != nil {
		t.Fatalf("TarCollections failed: %v", err)
	}

	// Verify tar paths
	for i, path := range resultPaths {
		if path != tarPaths[i] {
			t.Errorf("Result path %d = %s, want %s", i, path, tarPaths[i])
		}
	}

	// Verify all collections were tarred and cleaned up
	for i, coll := range collections {
		if !tarCalled[i] {
			t.Errorf("TarCollection was not called for collection %s", coll.Name)
		}
		if !cleanupCalled[i] {
			t.Errorf("CleanupCollectionDirectory was not called for collection %s", coll.Name)
		}
	}
	
	// Test backward compatibility - ZipCollections should call TarCollections
	TarCollection = func(ctx context.Context, collPath string) (string, error) {
		for i, coll := range collections {
			if collPath == coll.Path {
				tarPaths[i] = collPath + ".tar" // Change to make sure we can detect the call
				tarCalled[i] = false // Reset to track new calls
				return tarPaths[i], nil
			}
		}
		return "", nil
	}
	
	// Reset tracking arrays
	for i := range tarCalled {
		tarCalled[i] = false
		cleanupCalled[i] = false
	}
	
	// Call ZipCollections (which should now call TarCollections)
	resultPaths, err = ZipCollections(ctx, collections)
	if err != nil {
		t.Fatalf("ZipCollections compatibility function failed: %v", err)
	}
	
	// Verify all collections were processed by the compatibility wrapper
	for i, path := range resultPaths {
		if path != tarPaths[i] {
			t.Errorf("Compatibility function result path %d = %s, want %s", i, path, tarPaths[i])
		}
	}
}

// MockReader implements io.Reader for testing
type MockReader struct {
	Data     []byte
	Position int
}

func (m *MockReader) Read(p []byte) (n int, err error) {
	if m.Position >= len(m.Data) {
		return 0, io.EOF
	}

	n = copy(p, m.Data[m.Position:])
	m.Position += n
	return n, nil
}

func TestCollectionNameValidation(t *testing.T) {
	tests := []struct {
		name     string
		collName string
		expect   bool
	}{
		{"Valid name", "3A5", true},
		{"Valid lowercase", "3a5", true},
		{"Valid complex", "12Z26", true},
		{"Too short", "A5", false},
		{"Invalid first char", "A35", false},
		{"Invalid middle char", "353", false},
		{"Invalid last char", "3AX", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsCollectionName(tt.collName)
			if result != tt.expect {
				t.Errorf("IsCollectionName(%s) = %v, want %v", tt.collName, result, tt.expect)
			}
		})
	}
}