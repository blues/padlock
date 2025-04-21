// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blues/padlock/pkg/trace"
)

func TestValidateInputDirectory(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "directory-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a temporary file
	tempFile := filepath.Join(tempDir, "testfile.txt")
	if err := os.WriteFile(tempFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test cases
	tests := []struct {
		name        string
		inputDir    string
		expectError bool
	}{
		{"Valid directory", tempDir, false},
		{"Non-existent directory", filepath.Join(tempDir, "nonexistent"), true},
		{"File instead of directory", tempFile, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInputDirectory(ctx, tt.inputDir)

			if tt.expectError && err == nil {
				t.Errorf("Expected error for directory '%s' but got nil", tt.inputDir)
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error for directory '%s' but got: %v", tt.inputDir, err)
			}
		})
	}
}

func TestPrepareOutputDirectory(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "directory-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test cases
	tests := []struct {
		name               string
		outputDir          string
		clear              bool
		setupFunc          func() error
		expectError        bool
		checkNotEmptyAfter bool
	}{
		{
			name:        "New directory",
			outputDir:   filepath.Join(tempDir, "new"),
			clear:       false,
			setupFunc:   func() error { return nil },
			expectError: false,
		},
		{
			name:      "Existing empty directory",
			outputDir: filepath.Join(tempDir, "empty"),
			clear:     false,
			setupFunc: func() error {
				return os.MkdirAll(filepath.Join(tempDir, "empty"), 0755)
			},
			expectError: false,
		},
		{
			name:      "Non-empty directory without clear",
			outputDir: filepath.Join(tempDir, "nonempty_noclear"),
			clear:     false,
			setupFunc: func() error {
				dir := filepath.Join(tempDir, "nonempty_noclear")
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(dir, "file.txt"), []byte("test"), 0644)
			},
			expectError: true,
		},
		{
			name:      "Non-empty directory with clear",
			outputDir: filepath.Join(tempDir, "nonempty_clear"),
			clear:     true,
			setupFunc: func() error {
				dir := filepath.Join(tempDir, "nonempty_clear")
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(dir, "file.txt"), []byte("test"), 0644)
			},
			expectError:        false,
			checkNotEmptyAfter: true,
		},
		{
			name:      "File instead of directory",
			outputDir: filepath.Join(tempDir, "file"),
			clear:     false,
			setupFunc: func() error {
				return os.WriteFile(filepath.Join(tempDir, "file"), []byte("test"), 0644)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if err := tt.setupFunc(); err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			// Test
			err := PrepareOutputDirectory(ctx, tt.outputDir, tt.clear)

			// Check results
			if tt.expectError && err == nil {
				t.Errorf("Expected error for directory '%s' but got nil", tt.outputDir)
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error for directory '%s' but got: %v", tt.outputDir, err)
			}

			// If we didn't get an error, the directory should exist
			if err == nil {
				if _, err := os.Stat(tt.outputDir); os.IsNotExist(err) {
					t.Errorf("Output directory '%s' was not created", tt.outputDir)
				}
			}

			// If we cleared the directory, it should be empty
			if tt.clear && !tt.expectError && !tt.checkNotEmptyAfter {
				files, err := os.ReadDir(tt.outputDir)
				if err != nil {
					t.Fatalf("Failed to read directory: %v", err)
				}
				if len(files) > 0 {
					t.Errorf("Directory '%s' was not cleared, contains %d files", tt.outputDir, len(files))
				}
			}
		})
	}
}

func TestCreateCollectionDirectory(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "directory-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Test creating a new collection directory
	collName := "3A5"
	collPath, err := CreateCollectionDirectory(ctx, tempDir, collName)
	if err != nil {
		t.Fatalf("CreateCollectionDirectory failed: %v", err)
	}

	// Verify the directory was created
	expectedPath := filepath.Join(tempDir, collName)
	if collPath != expectedPath {
		t.Errorf("Expected collection path '%s', got '%s'", expectedPath, collPath)
	}

	if _, err := os.Stat(collPath); os.IsNotExist(err) {
		t.Errorf("Collection directory '%s' was not created", collPath)
	}
}
