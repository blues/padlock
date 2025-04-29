// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

package file

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/blues/padlock/pkg/trace"
)

// SerializeDirectoryToStream takes an input directory path and generates an io.Reader
// which is a 'tar' stream of the entire directory.
func SerializeDirectoryToStream(ctx context.Context, inputDir string) (io.ReadCloser, error) {
	log := trace.FromContext(ctx).WithPrefix("serialize")
	log.Debugf("Serializing directory to tar stream: %s", inputDir)
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()

		log.Debugf("Creating tar writer")
		tw := tar.NewWriter(pw)
		defer tw.Close()

		fileCount := 0
		totalBytes := int64(0)

		// Walk through the directory
		err := filepath.Walk(inputDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				log.Error(fmt.Errorf("error walking path %s: %w", path, walkErr))
				return walkErr
			}

			// Skip the input directory itself
			if path == inputDir {
				return nil
			}

			// Skip symlinks
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}

			// Get the relative path for the tar entry
			rel, err := filepath.Rel(inputDir, path)
			if err != nil {
				log.Error(fmt.Errorf("failed to determine relative path: %w", err))
				return err
			}

			// Create a tar header
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				log.Error(fmt.Errorf("tar FileInfoHeader for %s: %w", path, err))
				return err
			}
			header.Name = rel

			// Write the header to the tar stream
			if err := tw.WriteHeader(header); err != nil {
				log.Error(fmt.Errorf("tar WriteHeader for %s: %w", rel, err))
				return err
			}

			// For directories, we're done after writing the header
			if info.IsDir() {
				return nil
			}

			// Open the file to copy its contents
			f, err := os.Open(path)
			if err != nil {
				log.Error(fmt.Errorf("open file for tar %s: %w", path, err))
				return err
			}
			defer f.Close()

			// Copy the file data to the tar stream
			n, err := io.Copy(tw, f)
			if err != nil {
				log.Error(fmt.Errorf("io.Copy to tar for %s: %w", rel, err))
				return err
			}

			fileCount++
			totalBytes += n
			log.Infof("%s (%d bytes)", rel, n)

			return nil
		})

		if err != nil {
			log.Error(fmt.Errorf("error during directory serialization: %w", err))
			pw.CloseWithError(fmt.Errorf("error during directory serialization: %w", err))
			return
		}

		log.Debugf("Directory serialization complete: %d files, %d bytes", fileCount, totalBytes)
	}()

	return pr, nil
}

// DeserializeDirectoryFromStream takes a tar stream and extracts its contents
// to the specified output directory. It returns errors encountered during extraction.
func DeserializeDirectoryFromStream(ctx context.Context, outputDir string, r io.Reader, clearIfNotEmpty bool) error {
	log := trace.FromContext(ctx).WithPrefix("deserialize")
	log.Debugf("Deserializing to directory: %s", outputDir)

	// Ensure the output directory can be written to
	if err := prepareOutputDirectory(ctx, outputDir, clearIfNotEmpty); err != nil {
		log.Error(fmt.Errorf("failed to clear directory: %w", err))
		return err
	}

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Error(fmt.Errorf("failed to create output directory: %w", err))
		return err
	}

	log.Debugf("Directory prepared, now reading input stream")

	// Process extraction in a streaming manner
	done := make(chan error)
	go func() {
		defer close(done)

		// First, peek to check the format
		peekBuf := make([]byte, 512) // TAR header size
		n, err := io.ReadFull(r, peekBuf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Error(fmt.Errorf("error reading from input stream: %w", err))
			done <- err
			return
		}

		// Recreate the full stream with the peeked data
		fullStream := io.MultiReader(bytes.NewReader(peekBuf[:n]), r)

		// Small file handling (less than 512 bytes)
		if n < 512 {
			log.Infof("Input data is small (%d bytes), treating as raw data", n)

			// Check for gzip header (0x1f, 0x8b)
			if n >= 2 && peekBuf[0] == 0x1f && peekBuf[1] == 0x8b {
				log.Infof("Detected gzip header, setting up streaming decompression")

				// Set up streaming decompression
				gzr, err := gzip.NewReader(fullStream)
				if err != nil {
					log.Error(fmt.Errorf("failed to create gzip reader: %w", err))
					done <- err
					return
				}
				defer gzr.Close()

				// Handle small decompressed data
				decompBuffer := make([]byte, 4096)
				bytesRead, err := io.ReadFull(gzr, decompBuffer)

				// Check if it's a full buffer or we hit EOF or unexpected EOF
				if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
					log.Error(fmt.Errorf("error during initial decompression: %w", err))
					done <- err
					return
				}

				// If what we read looks like a TAR file (>= 512 bytes), treat it as one
				if bytesRead >= 512 {
					log.Infof("Decompressed data looks like a TAR file, processing as stream")

					// Process using streaming tar reader
					tarReader := tar.NewReader(io.MultiReader(bytes.NewReader(decompBuffer[:bytesRead]), gzr))
					if err := streamTarToDirectory(ctx, outputDir, tarReader, log); err != nil {
						done <- err
						return
					}
				} else {
					// Small non-TAR data, just save it directly
					outfile := filepath.Join(outputDir, "decoded_output.dat")
					f, err := os.Create(outfile)
					if err != nil {
						log.Error(fmt.Errorf("failed to create output file: %w", err))
						done <- err
						return
					}

					// First write what we've already read
					_, err = f.Write(decompBuffer[:bytesRead])
					if err != nil {
						f.Close()
						log.Error(fmt.Errorf("failed to write decompressed data: %w", err))
						done <- err
						return
					}

					// Then copy the rest
					written, err := io.Copy(f, gzr)
					f.Close()

					if err != nil {
						log.Error(fmt.Errorf("failed to copy decompressed data: %w", err))
						done <- err
						return
					}

					log.Infof("Wrote decompressed data to %s (%d bytes)", outfile, written+int64(bytesRead))
					fmt.Printf("\nDecoding completed successfully. Output saved to %s (%d bytes)\n",
						outfile, written+int64(bytesRead))
				}

				done <- nil
				return
			}

			// Small non-compressed file - save directly
			outfile := filepath.Join(outputDir, "decoded_data.txt")

			// Attempt to detect if this is text or binary
			isText := true
			for _, b := range peekBuf[:n] {
				if b < 32 && b != '\n' && b != '\r' && b != '\t' {
					isText = false
					break
				}
			}

			if !isText {
				outfile = filepath.Join(outputDir, "decoded_data.bin")
				log.Infof("Detected binary data, saving as binary file")
			} else {
				log.Infof("Detected text data, saving as text file")
			}

			f, err := os.Create(outfile)
			if err != nil {
				log.Error(fmt.Errorf("failed to create output file: %w", err))
				done <- err
				return
			}

			// First write what we've already read
			_, err = f.Write(peekBuf[:n])
			if err != nil {
				f.Close()
				log.Error(fmt.Errorf("failed to write data: %w", err))
				done <- err
				return
			}

			// Then copy any remaining data (unlikely for small files, but just in case)
			written, err := io.Copy(f, r)
			f.Close()

			if err != nil {
				log.Error(fmt.Errorf("failed to write data: %w", err))
				done <- err
				return
			}

			totalBytes := written + int64(n)
			log.Infof("Successfully wrote %d bytes to %s", totalBytes, outfile)
			fmt.Printf("\nDecoding completed successfully. Output saved to %s (%d bytes)\n", outfile, totalBytes)

			done <- nil
			return
		}

		// Check if it looks like a gzip-compressed file
		if peekBuf[0] == 0x1f && peekBuf[1] == 0x8b {
			log.Infof("Detected gzip header, setting up streaming decompression pipeline")

			// Set up streaming decompression
			gzr, err := gzip.NewReader(fullStream)
			if err != nil {
				log.Error(fmt.Errorf("failed to create gzip reader: %w", err))
				done <- err
				return
			}
			defer gzr.Close()

			// Process using streaming tar reader with decompressed data
			tarReader := tar.NewReader(gzr)
			if err := streamTarToDirectory(ctx, outputDir, tarReader, log); err != nil {
				done <- err
				return
			}
		} else {
			// Regular tar file (not compressed)
			log.Infof("Processing uncompressed tar stream")

			// Set up tar reader directly
			tarReader := tar.NewReader(fullStream)
			if err := streamTarToDirectory(ctx, outputDir, tarReader, log); err != nil {
				done <- err
				return
			}
		}

		done <- nil
	}()

	// Wait for extraction to complete
	err := <-done
	return err
}

// streamTarToDirectory extracts a tar stream to a directory using streaming I/O
// This helper function processes tar entries one by one without loading the entire tar file
// into memory, making it suitable for very large archives.
func streamTarToDirectory(ctx context.Context, outputDir string, tr *tar.Reader, log *trace.Tracer) error {
	fileCount := 0
	totalBytes := int64(0)
	progressInterval := 100 // Log progress every N files
	progressCounter := 0
	lastProgressTime := time.Now()
	progressUpdateInterval := 5 * time.Second // Minimum time between progress updates

	// Iterate through tar entries
	for {
		header, err := tr.Next()
		if err == io.EOF {
			if fileCount == 0 {
				log.Error(fmt.Errorf("no files found in tar archive"))
				return fmt.Errorf("no files found in tar archive")
			}
			break // End of tar archive
		}
		if err != nil {
			log.Error(fmt.Errorf("tar header read error: %w", err))
			return fmt.Errorf("tar header read error: %w", err)
		}

		// Get the full path for extraction
		outPath := filepath.Join(outputDir, header.Name)

		// Handle directory entries
		if header.Typeflag == tar.TypeDir {
			if log.IsVerbose() {
				log.Debugf("Creating directory: %s", outPath)
			}
			if err := os.MkdirAll(outPath, os.FileMode(header.Mode)); err != nil {
				log.Error(fmt.Errorf("failed to create directory %s: %w", outPath, err))
				return err
			}
			continue
		}

		// Create parent directory for files
		parentDir := filepath.Dir(outPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			log.Error(fmt.Errorf("failed to create parent directory for %s: %w", outPath, err))
			return err
		}

		// Create the file for writing
		if log.IsVerbose() {
			log.Debugf("Creating file: %s", outPath)
		}
		file, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
		if err != nil {
			log.Error(fmt.Errorf("failed to create file %s: %w", outPath, err))
			return err
		}

		// Copy file contents
		n, err := io.Copy(file, tr)
		file.Close()
		if err != nil {
			log.Error(fmt.Errorf("failed to write file %s: %w", outPath, err))
			return err
		}

		fileCount++
		totalBytes += n

		// Progress logging - don't spam the logs too much for large archives
		progressCounter++
		if progressCounter >= progressInterval || time.Since(lastProgressTime) > progressUpdateInterval {
			log.Infof("Extraction progress: %d files (%s)", fileCount, formatByteSize(totalBytes))
			progressCounter = 0
			lastProgressTime = time.Now()
		} else {
			log.Infof("Extracted: %s (%d bytes)", header.Name, n)
		}
	}

	log.Infof("Directory deserialization complete: %d files (%s)", fileCount, formatByteSize(totalBytes))
	return nil
}

// formatByteSize formats size in bytes to a human-readable string with units
func formatByteSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// prepareOutputDirectory ensures the output directory is empty for deserialization
func prepareOutputDirectory(ctx context.Context, dirPath string, clearIfNotEmpty bool) error {
	log := trace.FromContext(ctx).WithPrefix("deserialize")
	log.Debugf("Preparing output directory: %s (clear=%v)", dirPath, clearIfNotEmpty)

	// Create the directory if it doesn't exist
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		log.Debugf("Creating directory: %s", dirPath)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			log.Error(fmt.Errorf("failed to create directory: %w", err))
			return err
		}
		return nil
	}

	// Check if the directory is empty
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		log.Error(fmt.Errorf("failed to read directory: %w", err))
		return err
	}

	// If not empty, check if we should clear it
	if len(entries) > 0 {
		log.Debugf("Directory %s is not empty (%d entries)", dirPath, len(entries))
		if !clearIfNotEmpty {
			return nil
		}

		// Remove all entries
		log.Debugf("Removing %d entries from directory: %s", len(entries), dirPath)
		var clearErrors []string
		for _, entry := range entries {
			entryPath := filepath.Join(dirPath, entry.Name())
			log.Debugf("Removing: %s", entryPath)
			if err := os.RemoveAll(entryPath); err != nil {
				errMsg := fmt.Sprintf("failed to remove %s: %v", entryPath, err)
				log.Error(fmt.Errorf("%s", errMsg))
				clearErrors = append(clearErrors, errMsg)
			}
		}

		// Check if any errors occurred during clearing
		if len(clearErrors) > 0 {
			if len(clearErrors) <= 3 {
				log.Error(fmt.Errorf("failed to fully clear directory: %v", clearErrors))
				return fmt.Errorf("failed to fully clear directory: %v", clearErrors)
			}
			log.Error(fmt.Errorf("failed to fully clear directory: %v and %d more errors",
				clearErrors[:3], len(clearErrors)-3))
			return fmt.Errorf("failed to fully clear directory: %v and %d more errors",
				clearErrors[:3], len(clearErrors)-3)
		}

		// Verify the directory is now empty
		entries, err = os.ReadDir(dirPath)
		if err != nil {
			log.Error(fmt.Errorf("failed to recheck directory after clearing: %w", err))
			return err
		}
		if len(entries) > 0 {
			log.Error(fmt.Errorf("directory not empty after clearing, manual intervention required"))
			return fmt.Errorf("directory not empty after clearing, manual intervention required")
		}
	}

	log.Debugf("Directory %s is prepared", dirPath)
	return nil
}
