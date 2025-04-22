// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

package file

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/blues/padlock/pkg/trace"
)

func TestGetFormatter(t *testing.T) {
	// Test getting BIN formatter
	binFormatter := GetFormatter(FormatBin)
	if binFormatter == nil {
		t.Fatalf("GetFormatter(FormatBin) returned nil")
	}

	// Test getting PNG formatter
	pngFormatter := GetFormatter(FormatPNG)
	if pngFormatter == nil {
		t.Fatalf("GetFormatter(FormatPNG) returned nil")
	}

	// Make sure they're different types
	if _, ok := binFormatter.(*BinFormatter); !ok {
		t.Errorf("Expected BinFormatter for FormatBin")
	}

	if _, ok := pngFormatter.(*PngFormatter); !ok {
		t.Errorf("Expected PngFormatter for FormatPNG")
	}
}

func TestBinFormatter(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "binformatter-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test collection directory
	collPath := filepath.Join(tempDir, "3A5")
	if err := os.MkdirAll(collPath, 0755); err != nil {
		t.Fatalf("Failed to create collection dir: %v", err)
	}

	// Create a formatter
	formatter := &BinFormatter{}

	// Test data
	testData := []byte("hello world")

	// Test writing a chunk
	err = formatter.WriteChunk(ctx, collPath, 0, 1, testData)
	if err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	// Verify the file was created
	chunkPath := filepath.Join(collPath, "3A5_0001.bin")
	if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
		t.Fatalf("Chunk file was not created: %s", chunkPath)
	}

	// Read the chunk data
	data, err := formatter.ReadChunk(ctx, collPath, 0, 1)
	if err != nil {
		t.Fatalf("ReadChunk failed: %v", err)
	}

	// Verify the data matches
	if !bytes.Equal(data, testData) {
		t.Errorf("Read data does not match written data. Got %v, expected %v", data, testData)
	}

	// Test reading a non-existent chunk
	_, err = formatter.ReadChunk(ctx, collPath, 0, 999)
	if err == nil {
		t.Errorf("Expected error when reading non-existent chunk, got nil")
	}
}

func TestPNGFormatter(t *testing.T) {
	ctx := context.Background()
	tracer := trace.NewTracer("TEST", trace.LogLevelVerbose)
	ctx = trace.WithContext(ctx, tracer)

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "pngformatter-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test collection directory
	collPath := filepath.Join(tempDir, "3A5")
	if err := os.MkdirAll(collPath, 0755); err != nil {
		t.Fatalf("Failed to create collection dir: %v", err)
	}

	// Create a formatter
	formatter := &PngFormatter{}

	// Test data
	testData := []byte("hello world")

	// Test writing a chunk
	err = formatter.WriteChunk(ctx, collPath, 0, 1, testData)
	if err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	// Verify the file was created
	chunkPath := filepath.Join(collPath, "IMG3A5_0001.PNG")
	if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
		t.Fatalf("Chunk file was not created: %s", chunkPath)
	}

	// Read the chunk data
	data, err := formatter.ReadChunk(ctx, collPath, 0, 1)
	if err != nil {
		t.Fatalf("ReadChunk failed: %v", err)
	}

	// Verify the data matches
	if !bytes.Equal(data, testData) {
		t.Errorf("Read data does not match written data. Got %v, expected %v", data, testData)
	}

	// Test reading a non-existent chunk
	_, err = formatter.ReadChunk(ctx, collPath, 0, 999)
	if err == nil {
		t.Errorf("Expected error when reading non-existent chunk, got nil")
	}
}

func TestExtractDataFromPNG(t *testing.T) {
	// Create a PNG with some test data
	img := createSmallPNG()
	testData := []byte("test data for PNG extraction")

	var buf bytes.Buffer
	err := encodePNGWithData(&buf, img, testData)
	if err != nil {
		t.Fatalf("Failed to encode PNG with data: %v", err)
	}

	// Extract the data
	extracted, err := ExtractDataFromPNG(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Failed to extract data from PNG: %v", err)
	}

	// Verify the data matches
	if !bytes.Equal(extracted, testData) {
		t.Errorf("Extracted data does not match embedded data. Got %v, expected %v", extracted, testData)
	}
}

func TestEncodePNGWithDataErrors(t *testing.T) {
	// Test with invalid PNG
	invalidPNG := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // Just PNG signature
	_, err := ExtractDataFromPNG(bytes.NewReader(invalidPNG))
	if err == nil {
		t.Errorf("Expected error when extracting from invalid PNG, got nil")
	}

	// Test with PNG without rAWd chunk
	img := createSmallPNG()
	var buf bytes.Buffer
	err = writeMinimalPNG(&buf, img)
	if err != nil {
		t.Fatalf("Failed to write minimal PNG: %v", err)
	}

	_, err = ExtractDataFromPNG(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Errorf("Expected error when extracting from PNG without rAWd chunk, got nil")
	}
}

// Helper function to create a small PNG image
func writeMinimalPNG(w io.Writer, img image.Image) error {
	return png.Encode(w, img)
}

// Helper function to create a small test image
func createSmallPNG() image.Image {
	return image.NewRGBA(image.Rect(0, 0, 1, 1))
}
