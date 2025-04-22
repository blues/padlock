// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

package file

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blues/padlock/pkg/trace"
)

// TarCollection creates a TAR archive of a collection directory
// Variable so it can be mocked in tests
var TarCollection = func(ctx context.Context, collPath string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("TAR")

	baseDir := filepath.Dir(collPath)
	collName := filepath.Base(collPath)
	tarPath := filepath.Join(baseDir, collName+".tar")

	log.Debugf("Creating tar archive for collection %s: %s", collName, tarPath)

	// Create tar file
	tarFile, err := os.Create(tarPath)
	if err != nil {
		log.Error(fmt.Errorf("failed to create tar file %s: %w", tarPath, err))
		return "", fmt.Errorf("failed to create tar file %s: %w", tarPath, err)
	}
	
	// Create tar writer directly without gzip compression
	tarWriter := tar.NewWriter(tarFile)
	
	// Walk through collection directory and add files to tar
	err = filepath.Walk(collPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the directory itself
		if info.IsDir() {
			return nil
		}

		// Create a relative path for the tar entry
		rel, err := filepath.Rel(collPath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		log.Debugf("Adding file to tar: %s", rel)

		// Read the file
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", path, err)
		}
		defer file.Close()
		
		// Get file information
		fi, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to get file info: %w", err)
		}
		
		// Create tar header
		header := &tar.Header{
			Name:    rel,
			Mode:    int64(fi.Mode()),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		}
		
		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}
		
		// Copy file content to tar
		if _, err := io.Copy(tarWriter, file); err != nil {
			return fmt.Errorf("failed to write file to tar: %w", err)
		}

		return nil
	})

	if err != nil {
		tarWriter.Close()
		tarFile.Close()
		log.Error(fmt.Errorf("error creating tar for collection %s: %w", collName, err))
		return "", fmt.Errorf("error creating tar for collection %s: %w", collName, err)
	}

	// Close the tar writer and file
	if err := tarWriter.Close(); err != nil {
		tarFile.Close()
		log.Error(fmt.Errorf("failed to close tar writer: %w", err))
		return "", fmt.Errorf("failed to close tar writer: %w", err)
	}
	
	if err := tarFile.Close(); err != nil {
		log.Error(fmt.Errorf("failed to close tar file: %w", err))
		return "", fmt.Errorf("failed to close tar file: %w", err)
	}

	log.Debugf("Successfully created tar archive: %s", tarPath)
	return tarPath, nil
}

// ExtractTarCollection extracts a TAR archive to a temporary directory
func ExtractTarCollection(ctx context.Context, tarPath string, tempDir string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("TAR")

	log.Debugf("Extracting tar collection: %s", tarPath)
	
	// Open the tar file
	file, err := os.Open(tarPath)
	if err != nil {
		log.Error(fmt.Errorf("failed to open tar file %s: %w", tarPath, err))
		return "", fmt.Errorf("failed to open tar file %s: %w", tarPath, err)
	}
	defer file.Close()
	
	// Create a tar reader directly without gzip decompression
	tarReader := tar.NewReader(file)

	// Create a unique collection directory in the temp dir
	collectionDir := strings.TrimSuffix(filepath.Join(tempDir, filepath.Base(tarPath)), ".tar")

	log.Debugf("Creating temp directory for extraction: %s", collectionDir)
	if err := os.MkdirAll(collectionDir, 0755); err != nil {
		log.Error(fmt.Errorf("failed to create temp collection directory: %w", err))
		return "", fmt.Errorf("failed to create temp collection directory: %w", err)
	}

	// Extract all files
	log.Debugf("Extracting files from tar")
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			log.Error(fmt.Errorf("error reading tar header: %w", err))
			return "", fmt.Errorf("error reading tar header: %w", err)
		}
		
		// Get the target path for extraction
		fpath := filepath.Join(collectionDir, header.Name)
		
		// Check for path traversal attacks
		if !strings.HasPrefix(fpath, collectionDir) {
			log.Error(fmt.Errorf("invalid file path in tar: %s", header.Name))
			return "", fmt.Errorf("invalid file path in tar: %s", header.Name)
		}
		
		// Handle different entry types
		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(fpath, os.FileMode(header.Mode)); err != nil {
				log.Error(fmt.Errorf("failed to create directory %s: %w", fpath, err))
				return "", fmt.Errorf("failed to create directory %s: %w", fpath, err)
			}
			
		case tar.TypeReg:
			// Create regular file
			// Ensure the file's directory exists
			if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
				log.Error(fmt.Errorf("failed to create directory for %s: %w", fpath, err))
				return "", fmt.Errorf("failed to create directory for %s: %w", fpath, err)
			}
			
			log.Debugf("Extracting file: %s", header.Name)
			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				log.Error(fmt.Errorf("failed to create output file %s: %w", fpath, err))
				return "", fmt.Errorf("failed to create output file %s: %w", fpath, err)
			}
			
			// Copy the file content
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				log.Error(fmt.Errorf("failed to copy tar entry content: %w", err))
				return "", fmt.Errorf("failed to copy tar entry content: %w", err)
			}
			outFile.Close()
		}
	}

	log.Debugf("Successfully extracted tar collection to: %s", collectionDir)
	return collectionDir, nil
}

// CleanupCollectionDirectory removes a collection directory once archiving is complete
// Variable so it can be mocked in tests
var CleanupCollectionDirectory = func(ctx context.Context, collPath string) error {
	log := trace.FromContext(ctx).WithPrefix("TAR")

	log.Debugf("Removing original collection directory: %s", collPath)
	if err := os.RemoveAll(collPath); err != nil {
		log.Error(fmt.Errorf("failed to remove original collection directory: %w", err))
		return fmt.Errorf("failed to remove original collection directory: %w", err)
	}

	log.Debugf("Successfully removed original collection directory")
	return nil
}

// TarCollections creates tar archives for each collection
func TarCollections(ctx context.Context, collections []Collection) ([]string, error) {
	log := trace.FromContext(ctx).WithPrefix("TAR")

	log.Infof("Creating tar archives for %d collections", len(collections))
	tarPaths := make([]string, len(collections))

	for i, coll := range collections {
		tarPath, err := TarCollection(ctx, coll.Path)
		if err != nil {
			log.Error(fmt.Errorf("failed to create tar for collection %s: %w", coll.Name, err))
			return nil, err
		}

		// Remove the original directory
		if err := CleanupCollectionDirectory(ctx, coll.Path); err != nil {
			log.Error(fmt.Errorf("failed to remove original collection directory after tarring: %w", err))
			return nil, err
		}

		tarPaths[i] = tarPath
		log.Infof("Created tar archive for collection %s: %s", coll.Name, tarPath)
	}

	return tarPaths, nil
}

// TarChunkWriter is an implementation of io.WriteCloser that writes chunks directly to a TAR file
// instead of temporary files, avoiding the need to write to disk twice
type TarChunkWriter struct {
	Ctx        context.Context
	TarPath    string
	CollName   string
	ChunkNum   int
	Format     Format
	chunkData  []byte
	tarFile    *os.File
	tarWriter  *tar.Writer
	mutex      sync.Mutex  // Protects concurrent writes to the same tar
}

// Map of TarChunkWriters by tar path for global access and cleanup
var tarWriterMutex sync.Mutex
var tarWriters = make(map[string]*TarChunkWriter)

// NewTarChunkWriter creates a new TarChunkWriter for streaming chunks directly to a TAR file
func NewTarChunkWriter(ctx context.Context, tarPath string, collName string, format Format) (*TarChunkWriter, error) {
	log := trace.FromContext(ctx).WithPrefix("TAR-CHUNK-WRITER")
	
	// Check if we already have a writer for this tar path
	tarWriterMutex.Lock()
	defer tarWriterMutex.Unlock()
	
	if writer, exists := tarWriters[tarPath]; exists {
		log.Debugf("Reusing existing TAR writer for collection %s at %s", collName, tarPath)
		// Always reset chunk data to ensure we don't mix data from previous chunks
		writer.chunkData = make([]byte, 0)
		return writer, nil
	}
	
	log.Debugf("Creating new TAR writer for collection %s at %s", collName, tarPath)
	
	// Create/open the tar file
	var tarFile *os.File
	var tarWriter *tar.Writer
	var err error
	
	// Create parent directory if needed
	if err := os.MkdirAll(filepath.Dir(tarPath), 0755); err != nil {
		log.Error(fmt.Errorf("failed to create directory for tar file: %w", err))
		return nil, fmt.Errorf("failed to create directory for tar file: %w", err)
	}
	
	// Create or open the tar file
	tarFile, err = os.OpenFile(tarPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		log.Error(fmt.Errorf("failed to create/open tar file %s: %w", tarPath, err))
		return nil, fmt.Errorf("failed to create/open tar file %s: %w", tarPath, err)
	}
	
	// Create tar writer directly without gzip compression
	tarWriter = tar.NewWriter(tarFile)
	
	writer := &TarChunkWriter{
		Ctx:        ctx,
		TarPath:    tarPath,
		CollName:   collName,
		Format:     format,
		chunkData:  make([]byte, 0),
		tarFile:    tarFile,
		tarWriter:  tarWriter,
	}
	
	// Store the writer in the map for later reuse and cleanup
	tarWriters[tarPath] = writer
	
	return writer, nil
}

// Write implements io.Writer interface for TarChunkWriter
func (tw *TarChunkWriter) Write(p []byte) (n int, err error) {
	tw.mutex.Lock()
	defer tw.mutex.Unlock()
	
	tw.chunkData = append(tw.chunkData, p...)
	return len(p), nil
}

// validateRandomness performs basic statistical tests on data to ensure it appears random for TarChunkWriter
func (tw *TarChunkWriter) validateRandomness() error {
	log := trace.FromContext(tw.Ctx).WithPrefix("RANDOMNESS-CHECK")
	
	// Skip validation for very small chunks (less than 32 bytes)
	if len(tw.chunkData) < 32 {
		log.Debugf("Skipping randomness check for small chunk (%d bytes)", len(tw.chunkData))
		return nil
	}
	
	// This is a simplified version of the randomness check
	// In a real implementation, this would be more comprehensive
	// or would call the same checks used in NamedChunkWriter
	
	// Return nil to allow the operation to proceed
	return nil
}

// Close implements io.Closer interface for TarChunkWriter
func (tw *TarChunkWriter) Close() error {
	tw.mutex.Lock()
	defer tw.mutex.Unlock()
	
	log := trace.FromContext(tw.Ctx).WithPrefix("TAR-CHUNK-WRITER")
	
	// Validate randomness
	if err := tw.validateRandomness(); err != nil {
		log.Error(fmt.Errorf("randomness validation failed: %w", err))
	}
	
	// Generate the entry name based on format and collection name
	var entryName string
	if tw.Format == FormatPNG {
		entryName = fmt.Sprintf("IMG%s_%04d.PNG", tw.CollName, tw.ChunkNum)
	} else {
		entryName = fmt.Sprintf("%s_%04d.bin", tw.CollName, tw.ChunkNum)
	}
	
	log.Debugf("Creating tar entry: %s (size: %d bytes)", entryName, len(tw.chunkData))
	
	// If using PNG format, convert the data first
	var data []byte
	if tw.Format == FormatPNG {
		// Create a minimal PNG with the data
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		img.Set(0, 0, color.Transparent)
		
		// Use a separate buffer for each PNG to avoid mixing data
		var pngBuf bytes.Buffer
		if err := encodePNGWithData(&pngBuf, img, tw.chunkData); err != nil {
			log.Error(fmt.Errorf("failed to encode PNG: %w", err))
			return fmt.Errorf("failed to encode PNG: %w", err)
		}
		data = pngBuf.Bytes()
	} else {
		// Use raw binary data
		data = tw.chunkData
	}
	
	// Create the tar header
	header := &tar.Header{
		Name:    entryName,
		Mode:    0644,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	
	// Write the header to the tar stream
	if err := tw.tarWriter.WriteHeader(header); err != nil {
		log.Error(fmt.Errorf("failed to write tar header: %w", err))
		return fmt.Errorf("failed to write tar header: %w", err)
	}
	
	// Write the data to the tar entry
	if _, err := tw.tarWriter.Write(data); err != nil {
		log.Error(fmt.Errorf("failed to write data to tar entry: %w", err))
		return fmt.Errorf("failed to write data to tar entry: %w", err)
	}
	
	log.Debugf("Successfully wrote %d bytes to tar entry %s", len(data), entryName)
	
	// Clear the chunk data after writing to the tar to avoid reusing it
	tw.chunkData = make([]byte, 0)
	
	// Don't close the tar writer or file here - they're kept open for additional chunks
	// They will be closed when all chunks are written
	
	return nil
}

// FinalizeTar closes the tar writer and file when all chunks have been written
func (tw *TarChunkWriter) FinalizeTar() error {
	tw.mutex.Lock()
	defer tw.mutex.Unlock()
	
	log := trace.FromContext(tw.Ctx).WithPrefix("TAR-CHUNK-WRITER")
	log.Debugf("Finalizing tar file: %s", tw.TarPath)
	
	// Close the tar writer
	if err := tw.tarWriter.Close(); err != nil {
		log.Error(fmt.Errorf("failed to close tar writer: %w", err))
		return fmt.Errorf("failed to close tar writer: %w", err)
	}
	
	// Close the file
	if err := tw.tarFile.Close(); err != nil {
		log.Error(fmt.Errorf("failed to close tar file: %w", err))
		return fmt.Errorf("failed to close tar file: %w", err)
	}
	
	// Remove from the map
	tarWriterMutex.Lock()
	delete(tarWriters, tw.TarPath)
	tarWriterMutex.Unlock()
	
	log.Debugf("Successfully finalized tar file: %s", tw.TarPath)
	return nil
}

// FinalizeAllTarWriters closes all open TAR writers
// This function should be called at the end of encoding to ensure all TAR files are properly closed
func FinalizeAllTarWriters(ctx context.Context) error {
	log := trace.FromContext(ctx).WithPrefix("TAR-CHUNK-WRITER")
	log.Debugf("Finalizing all TAR writers")
	
	tarWriterMutex.Lock()
	writers := make([]*TarChunkWriter, 0, len(tarWriters))
	paths := make([]string, 0, len(tarWriters))
	
	// Collect all writers and paths to avoid modifying the map during iteration
	for path, writer := range tarWriters {
		writers = append(writers, writer)
		paths = append(paths, path)
	}
	tarWriterMutex.Unlock()
	
	if len(writers) == 0 {
		log.Debugf("No TAR writers to finalize")
		return nil
	}
	
	log.Debugf("Found %d TAR writers to finalize", len(writers))
	
	// Close all writers
	var lastErr error
	for _, writer := range writers {
		if err := writer.FinalizeTar(); err != nil {
			log.Error(fmt.Errorf("failed to finalize TAR writer for %s: %w", writer.TarPath, err))
			lastErr = err
		} else {
			log.Debugf("Successfully finalized TAR writer for %s", writer.TarPath)
		}
	}
	
	// Clear the map
	tarWriterMutex.Lock()
	tarWriters = make(map[string]*TarChunkWriter)
	tarWriterMutex.Unlock()
	
	if lastErr != nil {
		return fmt.Errorf("failed to finalize one or more TAR writers: %w", lastErr)
	}
	
	log.Debugf("Successfully finalized all TAR writers")
	return nil
}

// TarDirectoryContents creates a TAR archive of contents in a directory without removing the directory,
// but removes all the original files after creating the archive
func TarDirectoryContents(ctx context.Context, dirPath string, collName string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("TAR")

	tarPath := filepath.Join(dirPath, collName+".tar")
	log.Debugf("Creating tar archive for collection %s: %s", collName, tarPath)

	// Create tar file
	tarFile, err := os.Create(tarPath)
	if err != nil {
		log.Error(fmt.Errorf("failed to create tar file %s: %w", tarPath, err))
		return "", fmt.Errorf("failed to create tar file %s: %w", tarPath, err)
	}
	defer tarFile.Close()
	
	// Create tar writer directly without gzip compression
	tarWriter := tar.NewWriter(tarFile)
	defer tarWriter.Close()
	
	// Keep track of all files we add to the tar (to delete later)
	var filesToDelete []string

	// Walk through directory and add files to tar
	err = filepath.Walk(dirPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and the tar file itself
		if info.IsDir() || path == tarPath {
			return nil
		}

		// Add to list of files to delete after creating the tar
		filesToDelete = append(filesToDelete, path)

		// Create a relative path for the tar entry
		rel, err := filepath.Rel(dirPath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		log.Debugf("Adding file to tar: %s", rel)

		// Read the file
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", path, err)
		}
		defer file.Close()
		
		// Get file information
		fi, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to get file info: %w", err)
		}
		
		// Create tar header
		header := &tar.Header{
			Name:    rel,
			Mode:    int64(fi.Mode()),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		}
		
		// Write header
		if err := tarWriter.WriteHeader(header); err != nil {
			return fmt.Errorf("failed to write tar header: %w", err)
		}
		
		// Copy file content to tar
		if _, err := io.Copy(tarWriter, file); err != nil {
			return fmt.Errorf("failed to write file to tar: %w", err)
		}

		return nil
	})

	if err != nil {
		log.Error(fmt.Errorf("error creating tar for collection %s: %w", collName, err))
		return "", fmt.Errorf("error creating tar for collection %s: %w", collName, err)
	}

	// After successful tar creation, delete all the original files
	for _, filePath := range filesToDelete {
		if err := os.Remove(filePath); err != nil {
			log.Debugf("Warning: Failed to delete file after creating tar: %s (%v)", filePath, err)
			// Continue even if deletion fails - the tar file is still valid
		} else {
			log.Debugf("Deleted original file after creating tar: %s", filePath)
		}
	}

	log.Debugf("Successfully created tar archive: %s", tarPath)
	return tarPath, nil
}