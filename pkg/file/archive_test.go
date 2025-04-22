// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blues/padlock/pkg/trace"
)

func TestTarCollection(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "tar-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test collection directory
	collName := "3A5"
	collPath := filepath.Join(tempDir, collName)
	if err := os.MkdirAll(collPath, 0755); err != nil {
		t.Fatalf("Failed to create collection dir: %v", err)
	}

	// Create some test files in the collection
	testFiles := []string{
		"3A5_0001.bin",
		"3A5_0002.bin",
		"subdir/3A5_0003.bin",
	}

	for _, file := range testFiles {
		filePath := filepath.Join(collPath, file)
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create directory for test file: %v", err)
		}

		if err := os.WriteFile(filePath, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Test TarCollection
	tarPath, err := TarCollection(ctx, collPath)
	if err != nil {
		t.Fatalf("TarCollection failed: %v", err)
	}

	// Verify the tar file was created
	expectedTarPath := filepath.Join(tempDir, collName+".tar")
	if tarPath != expectedTarPath {
		t.Errorf("Expected tar path '%s', got '%s'", expectedTarPath, tarPath)
	}

	if _, err := os.Stat(tarPath); os.IsNotExist(err) {
		t.Errorf("Tar file '%s' was not created", tarPath)
	}
}

func TestExtractTarCollection(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "tar-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test collection directory
	collName := "3A5"
	collPath := filepath.Join(tempDir, collName)
	if err := os.MkdirAll(collPath, 0755); err != nil {
		t.Fatalf("Failed to create collection dir: %v", err)
	}

	// Create some test files in the collection
	testFiles := []string{
		"3A5_0001.bin",
		"3A5_0002.bin",
		"subdir/3A5_0003.bin",
	}

	for _, file := range testFiles {
		filePath := filepath.Join(collPath, file)
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("Failed to create directory for test file: %v", err)
		}

		if err := os.WriteFile(filePath, []byte("test content for "+file), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	// Create a tar file
	tarPath, err := TarCollection(ctx, collPath)
	if err != nil {
		t.Fatalf("TarCollection failed: %v", err)
	}

	// Remove the original collection directory to make sure we're really testing extraction
	if err := os.RemoveAll(collPath); err != nil {
		t.Fatalf("Failed to remove original collection dir: %v", err)
	}

	// Create a destination directory for extraction
	extractDir, err := os.MkdirTemp("", "tar-extract-*")
	if err != nil {
		t.Fatalf("Failed to create extract dir: %v", err)
	}
	defer os.RemoveAll(extractDir)

	// Test ExtractTarCollection
	extractedPath, err := ExtractTarCollection(ctx, tarPath, extractDir)
	if err != nil {
		t.Fatalf("ExtractTarCollection failed: %v", err)
	}

	// Verify the extracted directory was created
	expectedExtractedPath := filepath.Join(extractDir, collName)
	if extractedPath != expectedExtractedPath {
		t.Errorf("Expected extracted path '%s', got '%s'", expectedExtractedPath, extractedPath)
	}

	if _, err := os.Stat(extractedPath); os.IsNotExist(err) {
		t.Errorf("Extracted directory '%s' was not created", extractedPath)
	}

	// Verify all files were extracted
	for _, file := range testFiles {
		extractedFile := filepath.Join(extractedPath, file)
		if _, err := os.Stat(extractedFile); os.IsNotExist(err) {
			t.Errorf("Extracted file '%s' does not exist", extractedFile)
			continue
		}

		// Check file content
		content, err := os.ReadFile(extractedFile)
		if err != nil {
			t.Errorf("Failed to read extracted file '%s': %v", extractedFile, err)
			continue
		}

		expectedContent := "test content for " + file
		if string(content) != expectedContent {
			t.Errorf("Extracted file '%s' has wrong content: got '%s', expected '%s'",
				extractedFile, string(content), expectedContent)
		}
	}
}
