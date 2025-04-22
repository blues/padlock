# Padlock Usage Guide

This guide provides detailed instructions for using the Padlock utility to securely encode and decode data.

## Installation

Padlock is written in Go and can be built from source:

```bash
# Clone the repository
git clone https://github.com/blues/padlock.git
cd padlock

# Build the binary
go build -o padlock cmd/padlock/main.go

# Optionally, move to a directory in your PATH
sudo mv padlock /usr/local/bin/
```

## Basic Usage

Padlock has two main commands:

1. `encode`: Split input data into N collections with K-of-N threshold security
2. `decode`: Reconstruct original data from K or more collections

### Encoding Data

To encode data, use the following command structure:

```bash
padlock encode <inputDir> <outputDir> [options]
```

#### Required Parameters

- `<inputDir>`: Directory containing the data to be archived and encoded
- `<outputDir>`: Destination directory for the generated collection subdirectories

#### Options

- `-copies N`: Number of collections to create (must be between 2 and 26, default: 2)
- `-required K`: Minimum collections required for reconstruction (default: 2)
- `-format FORMAT`: Output format: bin or png (default: png)
- `-clear`: Clear output directory if not empty
- `-chunk SIZE`: Maximum candidate block size in bytes (default: 2MB)
- `-verbose`: Enable detailed debug output
- `-files`: Create individual files for each collection instead of TAR archives (default: creates TAR archives)

#### Examples

Create 3 collections where any 2 can reconstruct the data, in PNG format:
```bash
padlock encode ~/Documents/secret ~/Collections -copies 3 -required 2 -format png
```

Create 5 collections where any 3 are required, using TAR archives (default behavior):
```bash
padlock encode ~/Documents/top-secret ~/Collections -copies 5 -required 3
```

Enable verbose logging for debugging:
```bash
padlock encode ~/Documents/confidential ~/Collections -copies 4 -required 2 -verbose
```

### Decoding Data

To decode data, use the following command structure:

```bash
padlock decode <inputDir> <outputDir> [options]
```

#### Required Parameters

- `<inputDir>`: Root directory containing the collection subdirectories or ZIP files
- `<outputDir>`: Destination directory where the original data will be restored

#### Options

- `-clear`: Clear output directory if not empty
- `-verbose`: Enable detailed debug output

#### Examples

Reconstruct the original data from collections:
```bash
padlock decode ~/Collections ~/Restored
```

Clear the output directory before decoding:
```bash
padlock decode ~/Collections/subset ~/Restored -clear
```

Enable verbose logging for debugging:
```bash
padlock decode ~/Collections ~/Restored -verbose
```

## Advanced Usage

### Working with Large Datasets

When working with large datasets, consider the following tips:

1. **Adjust Chunk Size**: Use the `-chunk` option to control memory usage:
   ```bash
   padlock encode ~/LargeData ~/Collections -chunk 1048576  # 1MB chunks
   ```

2. **Use Binary Format**: For very large datasets, the binary format may be more efficient:
   ```bash
   padlock encode ~/LargeData ~/Collections -format bin
   ```

3. **Monitor Progress**: Use the `-verbose` flag to monitor progress during long operations:
   ```bash
   padlock encode ~/LargeData ~/Collections -verbose
   ```

### Collection Distribution Strategies

For maximum security, distribute collections across different storage locations:

1. **Physical Separation**: Store collections on different physical devices (USB drives, SD cards, etc.)
2. **Cloud Distribution**: Upload collections to different cloud storage providers
3. **Geographic Distribution**: Store collections in different physical locations
4. **Time-Based Distribution**: Transfer collections at different times to reduce correlation

### Handling TAR Collections

By default, Padlock creates TAR archives for each collection:

```bash
padlock encode ~/Documents/secret ~/Collections -copies 3 -required 2
```

This creates files like `3A5.tar`, `3B5.tar`, etc., which can be easily distributed.

If you prefer working with individual files instead of TAR archives, use the `-files` option:

```bash
padlock encode ~/Documents/secret ~/Collections -copies 3 -required 2 -files
```

For decoding, Padlock automatically detects and handles both formats:

```bash
# Decoding from TAR archives
padlock decode ~/Collections ~/Restored

# Decoding from directories containing individual files
padlock decode ~/Collections ~/Restored
```

Padlock intelligently processes TAR files as streams during both encoding and decoding, making it memory-efficient even for very large datasets.

## Best Practices

### Security Considerations

1. **Never Reuse Collections**: Each set of collections should be used exactly once. Reusing collections compromises the one-time pad security.

2. **Secure Deletion**: After decoding, securely delete collections that are no longer needed:
   ```bash
   # Linux example using shred
   find ~/Collections -type f -exec shred -uzn 3 {} \;
   ```

3. **Minimum Required Collections**: Set the `-required` parameter as high as practical for your use case to maximize security.

4. **Collection Naming**: The default collection names (e.g., "3A5", "3B5") indicate the threshold scheme (3 of 5 required). Consider renaming collections before distribution to obscure this information.

### Backup Strategies

1. **Create Extra Collections**: Generate more collections than the minimum required (e.g., 5 collections where only 3 are needed) to provide redundancy.

2. **Test Recovery**: Periodically test the recovery process with a subset of collections to ensure data can be successfully reconstructed.

3. **Document Parameters**: Keep a secure record of the parameters used for encoding (number of collections, required threshold) as this information is needed for decoding.

4. **Verify Collection Integrity**: Use checksums to verify the integrity of collections before attempting decoding:
   ```bash
   # Generate checksums
   find ~/Collections -type f -exec sha256sum {} \; > collection_checksums.txt
   
   # Verify checksums
   sha256sum -c collection_checksums.txt
   ```
