// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

// Package main provides the command-line interface for the padlock cryptographic system.
//
// Padlock is a K-of-N threshold one-time-pad cryptographic system that provides
// information-theoretic security. This means that:
// - Data is split into N collections (shares)
// - Any K collections can reconstruct the original data
// - K-1 or fewer collections reveal absolutely nothing about the original data
// - Security depends entirely on the quality of randomness used
//
// The system employs a combination of Shamir's Secret Sharing principles and one-time pad
// encryption. Unlike many cryptographic systems that rely on computational hardness,
// padlock provides information-theoretic security, which means it is mathematically
// provably secure regardless of an attacker's computational resources.
//
// The command-line interface supports two main operations:
// 1. encode: Split input data across N collections with K-of-N threshold security
// 2. decode: Reconstruct original data using K or more collections
//
// Each collection contains chunks of data that, when combined with chunks from other
// collections according to the threshold scheme, can reconstruct the original data.
// Internally, the system:
// - Serializes the input directory to a tar stream (optionally compressed)
// - Processes the stream in chunks through a secure random number generator
// - Applies one-time pad encryption with XOR operations
// - Distributes the encrypted data across N collections using combinatorial mathematics
// - Provides options for different output formats (binary or PNG)
//
// Usage examples:
//
//	# Create 3 collections where any 2 can reconstruct the data, in PNG format
//	padlock encode /path/to/input /path/to/output -copies 3 -required 2 -format png
//
//	# Reconstruct the original data from K or more collections
//	padlock decode /path/to/collections /path/to/output
//
//	# Enable verbose logging for debugging
//	padlock encode /path/to/input /path/to/output -verbose
//
//	# Create directories for each collection instead of ZIP files
//	padlock encode /path/to/input /path/to/output -unzip
//
// Security considerations:
// - Never reuse the same collections for different data (violates one-time pad security)
// - Keep collections physically separated to reduce risk of compromise
// - For maximum security, distribute collections through different channels/locations
// - The system is only as secure as its random number generator
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/blues/padlock/pkg/pad"
	"github.com/blues/padlock/pkg/padlock"
	"github.com/blues/padlock/pkg/trace"
)

// usage prints the command-line help information and exits.
//
// This function displays usage instructions for the padlock command-line tool,
// explaining the available commands, their parameters, and options.
// After displaying the help text, it exits with status code 1.
func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  padlock encode <inputDir> <outputDir> [-copies N] [-required REQUIRED] [-format bin|png] [-clear] [-chunk SIZE] [-verbose] [-unzip]
  padlock decode <inputDir> <outputDir> [-clear] [-verbose]

Commands:
  encode            Split input data into N collections with K-of-N threshold security
  decode            Reconstruct original data from K or more collections

Parameters:
  <inputDir>        Source directory containing data to encode or collections to decode
  <outputDir>       Destination directory for encoded collections or decoded data

Options:
  -copies N         Number of collections to create (must be between 2 and 26, default: 2)
  -required REQUIRED  Minimum collections required for reconstruction (default: 2)
  -format FORMAT    Output format: bin or png (default: png)
  -clear            Clear output directory if not empty
  -chunk SIZE       Maximum candidate block size in bytes (default: 2MB)
  -verbose          Enable detailed debug output
  -unzip            Create directories for each collection instead of zip files (default: creates zip files)

Examples:
  padlock encode ~/Documents/secret ~/Collections -copies 5 -required 3 -format png
  padlock decode ~/Collections/subset ~/Restored -clear
  padlock encode ~/Documents/top-secret ~/Collections -copies 5 -required 3 -verbose
`)
	os.Exit(1)
}

// main is the entry point for the padlock command-line tool.
//
// This function:
// 1. Parses command-line arguments and flags
// 2. Validates inputs and options
// 3. Creates appropriate configuration
// 4. Sets up logging and context
// 5. Executes the requested operation (encode or decode)
//
// The two main commands supported are:
// - encode: Splits input data across N collections with K-of-N threshold security
// - decode: Reconstructs original data using K or more collections
//
// Error handling:
// - Invalid parameters or flags trigger usage display
// - File access errors are reported with specific messages
// - Operational errors during encoding/decoding are reported with context
func main() {
	// Ensure a command is provided
	if len(os.Args) < 2 {
		usage()
	}

	cmd := os.Args[1]

	switch cmd {
	case "encode":
		if len(os.Args) < 4 {
			usage()
		}

		inputDir := os.Args[2]
		outputDir := os.Args[3]

		// Validate input directory
		inputStat, err := os.Stat(inputDir)
		if err != nil {
			if os.IsNotExist(err) {
				log.Fatalf("Error: Input directory does not exist: %s", inputDir)
			}
			log.Fatalf("Error: Cannot access input directory %s: %v", inputDir, err)
		}
		if !inputStat.IsDir() {
			log.Fatalf("Error: Input path is not a directory: %s", inputDir)
		}

		// Parse flags
		fs := flag.NewFlagSet("encode", flag.ExitOnError)
		nVal := fs.Int("copies", 2, "number of collections (must be between 2 and 26)")
		reqVal := fs.Int("required", 2, "minimum collections required for reconstruction")
		formatVal := fs.String("format", "png", "bin or png (default: png)")
		clearVal := fs.Bool("clear", false, "clear output directory if not empty")
		chunkVal := fs.Int("chunk", 2*1024*1024, "maximum candidate block size in bytes (default: 2MB)")
		verboseVal := fs.Bool("verbose", false, "enable detailed debug output (includes all trace information)")
		unzipVal := fs.Bool("unzip", false, "create directories for each collection instead of zip files")
		fs.Parse(os.Args[4:])

		// Validate flags
		if *nVal < 2 || *nVal > 26 {
			log.Fatalf("Error: Number of collections (-copies) must be between 2 and 26, got %d", *nVal)
		}
		if *reqVal < 2 {
			log.Printf("Warning: -required value %d is too small, using minimum value of 2", *reqVal)
			*reqVal = 2
		}
		if *reqVal > *nVal {
			log.Printf("Warning: -required value %d cannot be greater than number of collections (-copies) %d; adjusting to %d", *reqVal, *nVal, *nVal)
			*reqVal = *nVal
		}

		*formatVal = strings.ToLower(*formatVal)
		if *formatVal != "bin" && *formatVal != "png" {
			log.Fatalf("Error: -format must be 'bin' or 'png', got '%s'", *formatVal)
		}

		// Create config
		format := padlock.FormatPNG
		if *formatVal == "bin" {
			format = padlock.FormatBin
		}

		// Create context with tracer
		ctx := context.Background()
		logLevel := trace.LogLevelNormal
		if *verboseVal {
			logLevel = trace.LogLevelVerbose
		}
		log := trace.NewTracer("MAIN", logLevel)
		ctx = trace.WithContext(ctx, log)

		// Create RNG with the configured context
		rng := pad.NewDefaultRand(ctx)

		cfg := padlock.EncodeConfig{
			InputDir:        inputDir,
			OutputDir:       outputDir,
			N:               *nVal,
			K:               *reqVal,
			Format:          format,
			ChunkSize:       *chunkVal,
			RNG:             rng,
			ClearIfNotEmpty: *clearVal,
			Verbose:         *verboseVal,
			Compression:     padlock.CompressionGzip,
			ZipCollections:  !*unzipVal,
		}

		// Encode the directory
		if err := padlock.EncodeDirectory(ctx, cfg); err != nil {
			log.Fatal(fmt.Errorf("encode failed: %w", err))
		}

	case "decode":
		if len(os.Args) < 4 {
			usage()
		}

		inputDir := os.Args[2]
		outputDir := os.Args[3]

		// Validate input directory
		inputStat, err := os.Stat(inputDir)
		if err != nil {
			if os.IsNotExist(err) {
				log.Fatalf("Error: Input directory does not exist: %s", inputDir)
			}
			log.Fatalf("Error: Cannot access input directory %s: %v", inputDir, err)
		}
		// Input must be a directory for decoding
		if !inputStat.IsDir() {
			log.Fatalf("Error: Input path is not a directory: %s. The input should be a directory containing collection subdirectories or ZIP files.", inputDir)
		}

		// Parse flags
		fs := flag.NewFlagSet("decode", flag.ExitOnError)
		clearVal := fs.Bool("clear", false, "clear output directory if not empty")
		verboseVal := fs.Bool("verbose", false, "enable detailed debug output (includes all trace information)")
		fs.Parse(os.Args[4:])

		// Create context with tracer
		ctx := context.Background()
		logLevel := trace.LogLevelNormal
		if *verboseVal {
			logLevel = trace.LogLevelVerbose
		}
		log := trace.NewTracer("MAIN", logLevel)
		ctx = trace.WithContext(ctx, log)

		// Create RNG with the configured context
		rng := pad.NewDefaultRand(ctx)

		// Create config
		cfg := padlock.DecodeConfig{
			InputDir:        inputDir,
			OutputDir:       outputDir,
			RNG:             rng,
			Verbose:         *verboseVal,
			Compression:     padlock.CompressionGzip,
			ClearIfNotEmpty: *clearVal,
		}

		// Decode the directory
		if err := padlock.DecodeDirectory(ctx, cfg); err != nil {
			log.Fatal(fmt.Errorf("decode failed: %w", err))
		}

	default:
		usage()
	}
}
