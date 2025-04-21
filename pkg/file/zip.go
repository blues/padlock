// Copyright 2025 Ray Ozzie and his Mom. All rights reserved.

package file

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/blues/padlock/pkg/trace"
)

// ZipCollection creates a ZIP archive of a collection directory
func ZipCollection(ctx context.Context, collPath string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("ZIP")

	baseDir := filepath.Dir(collPath)
	collName := filepath.Base(collPath)
	zipPath := filepath.Join(baseDir, collName+".zip")

	log.Debugf("Creating zip archive for collection %s: %s", collName, zipPath)

	// Create zip file
	zipFile, err := os.Create(zipPath)
	if err != nil {
		log.Error(fmt.Errorf("failed to create zip file %s: %w", zipPath, err))
		return "", fmt.Errorf("failed to create zip file %s: %w", zipPath, err)
	}

	zw := zip.NewWriter(zipFile)

	// Walk through collection directory and add files to zip
	err = filepath.Walk(collPath, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the directory itself
		if info.IsDir() {
			return nil
		}

		// Create a relative path for the zip entry
		rel, err := filepath.Rel(collPath, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		log.Debugf("Adding file to zip: %s", rel)

		// Create a zip file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return fmt.Errorf("failed to create zip header: %w", err)
		}
		header.Name = rel
		header.Method = zip.Deflate

		// Create the file in the zip
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("failed to create zip entry: %w", err)
		}

		// Open the file to read its content
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", path, err)
		}
		defer file.Close()

		// Copy the file content to the zip entry
		_, err = io.Copy(writer, file)
		if err != nil {
			return fmt.Errorf("failed to write file to zip: %w", err)
		}

		return nil
	})

	if err != nil {
		zw.Close()
		zipFile.Close()
		log.Error(fmt.Errorf("error creating zip for collection %s: %w", collName, err))
		return "", fmt.Errorf("error creating zip for collection %s: %w", collName, err)
	}

	// Close the zip writer and file
	if err := zw.Close(); err != nil {
		zipFile.Close()
		log.Error(fmt.Errorf("failed to close zip writer: %w", err))
		return "", fmt.Errorf("failed to close zip writer: %w", err)
	}
	if err := zipFile.Close(); err != nil {
		log.Error(fmt.Errorf("failed to close zip file: %w", err))
		return "", fmt.Errorf("failed to close zip file: %w", err)
	}

	log.Debugf("Successfully created zip archive: %s", zipPath)
	return zipPath, nil
}

// ExtractZipCollection extracts a ZIP archive to a temporary directory
func ExtractZipCollection(ctx context.Context, zipPath string, tempDir string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("ZIP")

	log.Debugf("Extracting zip collection: %s", zipPath)
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		log.Error(fmt.Errorf("failed to open zip file %s: %w", zipPath, err))
		return "", fmt.Errorf("failed to open zip file %s: %w", zipPath, err)
	}
	defer r.Close()

	// Create a unique collection directory in the temp dir
	collectionDir := strings.TrimSuffix(filepath.Join(tempDir, filepath.Base(zipPath)), ".zip")

	log.Debugf("Creating temp directory for extraction: %s", collectionDir)
	if err := os.MkdirAll(collectionDir, 0755); err != nil {
		log.Error(fmt.Errorf("failed to create temp collection directory: %w", err))
		return "", fmt.Errorf("failed to create temp collection directory: %w", err)
	}

	// Extract all files
	log.Debugf("Extracting files from zip")
	for _, f := range r.File {
		fpath := filepath.Join(collectionDir, f.Name)

		// Ensure the file's directory exists
		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			log.Error(fmt.Errorf("failed to create directory for %s: %w", fpath, err))
			return "", fmt.Errorf("failed to create directory for %s: %w", fpath, err)
		}

		// Skip if directory
		if f.FileInfo().IsDir() {
			continue
		}

		log.Debugf("Extracting file: %s", f.Name)
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			log.Error(fmt.Errorf("failed to create output file %s: %w", fpath, err))
			return "", fmt.Errorf("failed to create output file %s: %w", fpath, err)
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			log.Error(fmt.Errorf("failed to open zip entry: %w", err))
			return "", fmt.Errorf("failed to open zip entry: %w", err)
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			log.Error(fmt.Errorf("failed to copy zip entry content: %w", err))
			return "", fmt.Errorf("failed to copy zip entry content: %w", err)
		}
	}

	log.Debugf("Successfully extracted zip collection to: %s", collectionDir)
	return collectionDir, nil
}

// CleanupCollectionDirectory removes a collection directory once zipping is complete
func CleanupCollectionDirectory(ctx context.Context, collPath string) error {
	log := trace.FromContext(ctx).WithPrefix("ZIP")

	log.Debugf("Removing original collection directory: %s", collPath)
	if err := os.RemoveAll(collPath); err != nil {
		log.Error(fmt.Errorf("failed to remove original collection directory: %w", err))
		return fmt.Errorf("failed to remove original collection directory: %w", err)
	}

	log.Debugf("Successfully removed original collection directory")
	return nil
}
