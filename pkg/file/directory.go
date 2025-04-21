package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blues/padlock/pkg/trace"
)

// ValidateInputDirectory checks if the input directory exists and is a directory
func ValidateInputDirectory(ctx context.Context, inputDir string) error {
	log := trace.FromContext(ctx).WithPrefix("FILE")

	log.Debugf("Validating input directory: %s", inputDir)

	inputStat, err := os.Stat(inputDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Error(fmt.Errorf("input directory does not exist: %s", inputDir))
			return fmt.Errorf("input directory does not exist: %s", inputDir)
		}
		log.Error(fmt.Errorf("cannot access input directory %s: %v", inputDir, err))
		return fmt.Errorf("cannot access input directory %s: %v", inputDir, err)
	}

	if !inputStat.IsDir() {
		log.Error(fmt.Errorf("input path is not a directory: %s", inputDir))
		return fmt.Errorf("input path is not a directory: %s", inputDir)
	}

	log.Debugf("Input directory is valid: %s", inputDir)
	return nil
}

// PrepareOutputDirectory ensures the output directory exists and is empty if clear is true
func PrepareOutputDirectory(ctx context.Context, outputDir string, clear bool) error {
	log := trace.FromContext(ctx).WithPrefix("FILE")

	log.Debugf("Preparing output directory: %s (clear=%v)", outputDir, clear)

	// Check if the directory exists
	exists := true
	stat, err := os.Stat(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			exists = false
		} else {
			log.Error(fmt.Errorf("cannot access output directory %s: %w", outputDir, err))
			return fmt.Errorf("cannot access output directory %s: %w", outputDir, err)
		}
	} else if !stat.IsDir() {
		log.Error(fmt.Errorf("output path exists but is not a directory: %s", outputDir))
		return fmt.Errorf("output path exists but is not a directory: %s", outputDir)
	}

	// If the directory exists and clear is true, remove everything in it
	if exists && clear {
		log.Debugf("Clearing output directory: %s", outputDir)

		// Read the directory
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			log.Error(fmt.Errorf("failed to read output directory: %w", err))
			return fmt.Errorf("failed to read output directory: %w", err)
		}

		// Remove each entry
		for _, entry := range entries {
			entryPath := filepath.Join(outputDir, entry.Name())
			log.Debugf("Removing: %s", entryPath)

			if err := os.RemoveAll(entryPath); err != nil {
				log.Error(fmt.Errorf("failed to remove %s: %w", entryPath, err))
				return fmt.Errorf("failed to remove %s: %w", entryPath, err)
			}
		}

		log.Debugf("Output directory cleared: %s", outputDir)
	} else if exists && !clear {
		// Check if the directory is empty
		entries, err := os.ReadDir(outputDir)
		if err != nil {
			log.Error(fmt.Errorf("failed to read output directory: %w", err))
			return fmt.Errorf("failed to read output directory: %w", err)
		}

		if len(entries) > 0 {
			var fileList string
			remainingCount := 0

			for i, entry := range entries {
				if i < 5 {
					fileList += fmt.Sprintf("\n  - %s", entry.Name())
				} else {
					remainingCount++
				}
			}

			errMsg := fmt.Sprintf("Output directory is not empty. Use -clear to clear the output directory.%s", fileList)
			if remainingCount > 0 {
				errMsg += fmt.Sprintf("\n  ... and %d more files/directories", remainingCount)
			}

			log.Error(fmt.Errorf("%s", errMsg))
			return fmt.Errorf("%s", errMsg)
		}

		log.Debugf("Output directory is empty: %s", outputDir)
	} else {
		// Create the directory
		log.Debugf("Creating output directory: %s", outputDir)

		if err := os.MkdirAll(outputDir, 0755); err != nil {
			log.Error(fmt.Errorf("failed to create output directory: %w", err))
			return fmt.Errorf("failed to create output directory: %w", err)
		}

		log.Debugf("Output directory created: %s", outputDir)
	}

	return nil
}

// CreateCollectionDirectory creates a collection directory
func CreateCollectionDirectory(ctx context.Context, baseDir string, collectionName string) (string, error) {
	log := trace.FromContext(ctx).WithPrefix("FILE")

	collPath := filepath.Join(baseDir, collectionName)
	log.Debugf("Creating collection directory: %s", collPath)

	if err := os.MkdirAll(collPath, 0755); err != nil {
		log.Error(fmt.Errorf("failed to create collection directory %s: %w", collPath, err))
		return "", fmt.Errorf("failed to create collection directory %s: %w", collPath, err)
	}

	log.Debugf("Collection directory created: %s", collPath)
	return collPath, nil
}
