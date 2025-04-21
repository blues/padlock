// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

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

	"github.com/blues/padlock/pkg/trace"
)

// SerializeDirectoryToStream takes an input directory path and generates an io.Reader
// which is a 'tar' stream of the entire directory.
func SerializeDirectoryToStream(ctx context.Context, inputDir string) (io.ReadCloser, error) {
	log := trace.FromContext(ctx).WithPrefix("SERIALIZE")
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
			log.Debugf("Added to tar: %s (%d bytes)", rel, n)

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
	log := trace.FromContext(ctx).WithPrefix("DESERIALIZE")
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

	// Read a small buffer to check if it looks like a tar file
	// TAR files start with a 512-byte header
	peekBuf := make([]byte, 512)
	n, err := r.Read(peekBuf)
	if err != nil && err != io.EOF {
		log.Error(fmt.Errorf("error reading from input stream: %w", err))
		return fmt.Errorf("error reading from input stream: %w", err)
	}

	if n < 512 {
		log.Infof("Input data is small (%d bytes), treating as raw data", n)

		// First, try to see if it looks like a gzip-compressed tar file (even if small)
		if n >= 2 && peekBuf[0] == 0x1f && peekBuf[1] == 0x8b {
			log.Infof("Detected gzip header, attempting to decompress")

			// Try to decompress it
			gzr, err := gzip.NewReader(bytes.NewReader(peekBuf[:n]))
			if err != nil {
				log.Error(fmt.Errorf("detected gzip header but failed to create reader: %w", err))
			} else {
				// Successfully created a gzip reader, read the decompressed data
				decompressed, err := io.ReadAll(gzr)
				gzr.Close()
				if err != nil {
					log.Error(fmt.Errorf("failed to decompress gzip data: %w", err))
				} else {
					log.Infof("Successfully decompressed %d bytes to %d bytes", n, len(decompressed))

					// Check if it's a tar file
					if len(decompressed) >= 512 {
						log.Infof("Decompressed data is large enough to be a tar file, using tar reader")
						tarReader := tar.NewReader(bytes.NewReader(decompressed))

						fileCount := 0
						for {
							header, err := tarReader.Next()
							if err == io.EOF {
								break
							}
							if err != nil {
								log.Error(fmt.Errorf("tar reading error: %w", err))
								break
							}

							// Get the output path
							outPath := filepath.Join(outputDir, header.Name)

							// Create parent directory
							if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
								log.Error(fmt.Errorf("failed to create directory for %s: %w", outPath, err))
								continue
							}

							// Create the file
							f, err := os.Create(outPath)
							if err != nil {
								log.Error(fmt.Errorf("failed to create file %s: %w", outPath, err))
								continue
							}

							// Copy the contents
							written, err := io.Copy(f, tarReader)
							f.Close()
							if err != nil {
								log.Error(fmt.Errorf("failed to write to %s: %w", outPath, err))
							} else {
								log.Infof("Extracted %s (%d bytes)", header.Name, written)
								fileCount++
							}
						}

						if fileCount > 0 {
							log.Infof("Successfully extracted %d files from tar archive", fileCount)
							return nil
						}
					}

					// Not a valid tar or no files extracted, just save the decompressed data
					outfile := filepath.Join(outputDir, "decoded_output.dat")
					if err := os.WriteFile(outfile, decompressed, 0644); err != nil {
						log.Error(fmt.Errorf("failed to write decompressed data: %w", err))
					} else {
						log.Infof("Wrote decompressed data to %s (%d bytes)", outfile, len(decompressed))
						fmt.Printf("\nDecoding completed successfully. Output saved to %s (%d bytes)\n",
							outfile, len(decompressed))
						return nil
					}
				}
			}
		}

		// Save the data directly to a file in the output directory - always use a consistent name
		outfile := filepath.Join(outputDir, "decoded_data.txt")

		// Attempt to detect if this is text or binary
		isText := true
		for _, b := range peekBuf[:n] {
			if b < 32 && b != '\n' && b != '\r' && b != '\t' {
				isText = false
				break
			}
		}

		if isText {
			log.Infof("Detected text data, saving as text file")
			// If it looks like text, save it as-is
			if err := os.WriteFile(outfile, peekBuf[:n], 0644); err != nil {
				log.Error(fmt.Errorf("failed to write decoded text: %w", err))
				return fmt.Errorf("failed to write decoded text: %w", err)
			}
		} else {
			// For binary data, save it as a binary file
			outfile = filepath.Join(outputDir, "decoded_data.bin")
			log.Infof("Detected binary data, saving as binary file")
			if err := os.WriteFile(outfile, peekBuf[:n], 0644); err != nil {
				log.Error(fmt.Errorf("failed to write decoded binary: %w", err))
				return fmt.Errorf("failed to write decoded binary: %w", err)
			}
		}

		log.Infof("Successfully wrote %d bytes to %s", n, outfile)
		fmt.Printf("\nDecoding completed successfully. Output saved to %s (%d bytes)\n", outfile, n)
		return nil
	}

	// Create a new reader that first returns our peeked data, then the rest
	combinedReader := io.MultiReader(bytes.NewReader(peekBuf[:n]), r)
	tr := tar.NewReader(combinedReader)

	fileCount := 0
	totalBytes := int64(0)

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
			// Create a sample file with the data we've seen
			samplePath := filepath.Join(outputDir, "invalid_tar_sample.dat")
			if err := os.WriteFile(samplePath, peekBuf[:n], 0644); err != nil {
				log.Debugf("Failed to write invalid tar sample: %v", err)
			} else {
				log.Debugf("Wrote invalid tar sample to %s", samplePath)
			}
			return fmt.Errorf("tar header read error: %w", err)
		}

		// Get the full path for extraction
		outPath := filepath.Join(outputDir, header.Name)

		// Handle directory entries
		if header.Typeflag == tar.TypeDir {
			log.Debugf("Creating directory: %s", outPath)
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
		log.Debugf("Creating file: %s", outPath)
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
		log.Debugf("Extracted: %s (%d bytes)", header.Name, n)
	}

	log.Debugf("Directory deserialization complete: %d files, %d bytes", fileCount, totalBytes)
	return nil
}

// prepareOutputDirectory ensures the output directory is empty for deserialization
func prepareOutputDirectory(ctx context.Context, dirPath string, clearIfNotEmpty bool) error {
	log := trace.FromContext(ctx).WithPrefix("DESERIALIZE")
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
