# Padlock Architecture

Padlock is built with a modular architecture consisting of several key components that work together to provide secure data encoding and decoding.

## System Components

### 1. Command-Line Interface

The CLI, implemented in `cmd/padlock/main.go`, serves as the entry point for user interaction with the Padlock system. It handles:

- Command-line argument parsing and validation
- Parameter configuration and validation
- Coordination of high-level encoding and decoding operations
- Error reporting and user feedback

The CLI supports two main commands:
- `encode`: Split input data across N collections with K-of-N threshold security
- `decode`: Reconstruct original data using K or more collections

### 2. Core Cryptographic Engine

The core cryptographic engine, primarily implemented in the `pkg/pad` package, is responsible for the mathematical operations that provide the threshold security. Key components include:

#### Pad Creation (`pkg/pad/pad.go`)

This component generates the mathematical structure for distributing data across collections. It:
- Creates the combinatorial mapping between collections and chunks
- Ensures that any K collections can reconstruct the data
- Manages the distribution of encoded chunks across collections

#### Random Number Generation (`pkg/pad/rng.go`)

The RNG component is critical for security and implements a multi-source architecture combining five independent entropy sources:

1. Go's built-in `crypto/rand` package
2. System entropy from `/dev/urandom`
3. Time-based entropy
4. Hardware-specific entropy
5. Process-specific entropy

This defense-in-depth approach ensures high-quality randomness even if some sources are compromised.

#### XOR Operations

The core of the one-time pad encryption is implemented using simple XOR operations, which provide perfect secrecy when combined with truly random keys that are never reused.

### 3. File System Layer

The file system layer, implemented in the `pkg/file` package, handles all interactions with the file system:

#### Collection Management

This component creates and manages collections as directories or ZIP archives:
- `file/collection.go`: Defines the structure and operations for collections
- `file/directory.go`: Handles directory operations for collections
- `file/zip.go`: Provides ZIP archive support for collections

#### Format Handling

Padlock supports multiple output formats for storing encoded data:
- `file/format.go`: Defines the interface for different formats
- Binary format: Stores data chunks directly as binary files
- PNG format: Stores data chunks as PNG images with CRC validation

#### Serialization

The serialization components convert directories to/from tar streams for processing:
- `file/serialize.go`: Implements directory serialization and deserialization
- `file/compress.go`: Provides compression and decompression functionality

### 4. Process Orchestration

The process orchestration layer, implemented in `pkg/padlock/padlock.go`, coordinates the overall encoding and decoding processes:

#### Streaming Pipeline

Sets up the data processing pipeline that:
1. Serializes input directories to tar streams
2. Optionally compresses the serialized data
3. Processes the data through the one-time pad encoder/decoder
4. Distributes encoded chunks across collections or reconstructs original data

#### Chunk Management

Manages the chunking of data for processing, ensuring that:
- Chunks are of appropriate size for efficient processing
- Chunk metadata is properly maintained
- Chunks are correctly distributed across collections

#### Error Handling

Provides robust error detection and reporting throughout the pipeline, with context-aware error messages that help diagnose issues.

## Data Flow

### Encoding Process

1. **Input Validation**: Verify input and output directories
2. **Pad Creation**: Create cryptographic pad with specified K-of-N parameters
3. **Collection Setup**: Create collection directories for encoded data
4. **Serialization**: Convert input directory to tar stream
5. **Compression**: Optionally compress serialized data
6. **Chunk Processing**: Process data in chunks through the encoder
   - Generate random one-time pads for each chunk
   - XOR input data with pads to create ciphertext
   - Distribute results across collections
7. **Output Formatting**: Write chunks to collections in specified format
8. **ZIP Creation**: Optionally create ZIP archives for collections

### Decoding Process

1. **Input Validation**: Verify input and output directories
2. **Collection Discovery**: Locate and load available collections
3. **Reader Setup**: Create readers for each collection
4. **Pad Creation**: Create pad instance for decoding
5. **Chunk Processing**: Process collections through the decoder
   - Read chunks from collections
   - Combine chunks according to threshold scheme
   - Reconstruct original data
6. **Decompression**: Optionally decompress data
7. **Deserialization**: Convert tar stream back to directory structure

## Component Interactions

The components interact through well-defined interfaces:

- **CLI → Padlock**: The CLI calls the high-level `EncodeDirectory` and `DecodeDirectory` functions in the padlock package.
- **Padlock → Pad**: The padlock package uses the pad package for the core cryptographic operations.
- **Padlock → File**: The padlock package uses the file package for file system operations.
- **Pad → File**: The pad package uses callback functions provided by the padlock package to write chunks to the file system.

This modular design allows for:
- Clear separation of concerns
- Easier testing and maintenance
- Potential for future extensions (e.g., new output formats, compression methods)
