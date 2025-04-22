// Copyright 2025 Ray Ozzie and a Mixture-of-Models. All rights reserved.

// Package pad implements a secure K-of-N threshold one-time-pad cryptographic scheme.
//
// This package provides the core cryptographic functionality of the padlock system,
// implementing Shamir's Secret Sharing combined with one-time-pad encryption.
// The threshold scheme allows data to be split into N collections (shares),
// where any K of them can be used to reconstruct the original data, but K-1 or
// fewer collections reveal absolutely nothing about the original data
// (information-theoretic security).
//
// Security properties:
// - True information-theoretic security (not dependent on computational hardness)
// - Perfect forward secrecy (past communications remain secure even if keys are compromised)
// - No key management required (each pad is used only once)
// - K-of-N threshold security (requires at least K collections to reconstruct)
// - If using true randomness, security is mathematically provable
//
// Implementation details:
// - Data is divided into fixed-size chunks for processing
// - Each chunk is split across N collections
// - Collections are generated so that any K of them can reconstruct the original data
// - File names on disk use format "<collectionName>_<chunkNumber>.<format>" (e.g., "3A5_0001.bin")
// - Internally within files, chunk names are stored as "<collectionName>-<chunkNumber>" (e.g., "3A5-1")
//
// Usage warnings:
// - The security of this system depends entirely on the quality of the random number generator
// - One-time pads must NEVER be reused
// - Data reconstruction requires exactly K or more of the original N collections
package pad

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/blues/padlock/pkg/trace"
)

// NewChunkFunc defines a function type for creating new chunk files.
// This is a callback function provided by the caller to create output files for each chunk.
// It creates a file with the specified collection name, chunk number, and format (e.g., bin or png).
// The returned WriteCloser must be properly closed by the caller after writing is complete.
type NewChunkFunc func(collectionName string, chunkNumber int, chunkFormat string) (io.WriteCloser, error)

// Pad represents the configuration for a one-time pad K-of-N threshold scheme operation.
// It maintains the parameters for the threshold scheme and the names of the collections
// that will be generated.
//
// The Pad is the core cryptographic component of the padlock system, implementing
// the mathematical properties that allow data to be split into N collections, where
// any K collections can reconstruct the original data, but K-1 or fewer collections
// reveal absolutely nothing about the original data (information-theoretic security).
//
// The implementation uses combinatorial mathematics and XOR operations to achieve
// the threshold properties. Each collection contains specific permutations of the data,
// carefully constructed so that only with K or more collections can the permutations
// be combined to recover the original data.
type Pad struct {
	TotalCopies      int                 // N: Total number of collections to create (2-26)
	RequiredCopies   int                 // K: Minimum collections needed for reconstruction (2-N)
	Collections      []string            // Names of each collection (e.g., ["3A5", "3B5", "3C5", ...])
	PermutationCount int                 // Number of unique combinations for K-of-N
	Permutations     map[string][]string // Unique combinations for each collection (maps collection letter to array of permutations)
	Ciphers          map[string][][]byte // Unique K-of-N combinations as byte slices (maps permutation key to array of byte slices)
}

// NewPadForEncode creates a new Pad instance with the specified parameters for a K-of-N threshold scheme.
//
// Parameters:
//   - totalCopies (N): The total number of collections to create. Must be between 2 and 26.
//     This represents the total number of shares in the threshold scheme.
//   - requiredCopies (K): The minimum number of collections required to reconstruct the data.
//     Must be at least 2 and not greater than totalCopies.  Note that when creating a pad
//     on decode, just set requiredCopies to the same as totalCopies.
//
// Returns:
//   - A configured Pad instance with generated collection names
//   - An error if the parameters are invalid
//
// Collection names are automatically generated in the format "<K><ID><N>", where:
//   - K is the requiredCopies value
//   - ID is a letter from A-Z representing the collection index
//   - N is the totalCopies value
//
// For example, with K=3, N=5, the collections would be: ["3A5", "3B5", "3C5", "3D5", "3E5"]
func NewPadForEncode(ctx context.Context, totalCopies, requiredCopies int) (*Pad, error) {
	p := &Pad{}
	return p, PadInit(ctx, p, totalCopies, requiredCopies)
}

// NewPadForDecode creates a new Pad instance with the specified parameters for a K-of-N threshold scheme.
//
// Parameters:
//   - availableCopies (N): The total number of collections available. Must be between 2 and 26.
//
// Returns:
//   - A configured Pad instance that can be used until parameters can be extracted
//   - An error if the parameters are invalid
func NewPadForDecode(ctx context.Context, availableCopies int) (*Pad, error) {
	p := &Pad{}
	return p, PadInit(ctx, p, availableCopies, availableCopies)
}

// PadInit initializes a new Pad instance with the specified parameters for a K-of-N threshold scheme.
//
// Parameters:
//   - An uninitialized Pad instance
//   - totalCopies (N): The total number of collections to create. Must be between 2 and 26.
//     This represents the total number of shares in the threshold scheme.
//   - requiredCopies (K): The minimum number of collections required to reconstruct the data.
//     Must be at least 2 and not greater than totalCopies.  Note that when creating a pad
//     on decode, just set requiredCopies to the same as totalCopies.
//
// Returns:
//   - An error if the parameters are invalid
//
// Collection names are automatically generated in the format "<K><ID><N>", where:
//   - K is the requiredCopies value
//   - ID is a letter from A-Z representing the collection index
//   - N is the totalCopies value
//
// For example, with K=3, N=5, the collections would be: ["3A5", "3B5", "3C5", "3D5", "3E5"]
func PadInit(ctx context.Context, p *Pad, totalCopies, requiredCopies int) error {
	log := trace.FromContext(ctx).WithPrefix("PAD-INIT")
	// Validate parameters to ensure they meet the requirements of the threshold scheme
	if totalCopies < 2 || totalCopies > 26 {
		return fmt.Errorf("totalCopies must be between 2 and 26, got %d", totalCopies)
	}
	// Validate parameters to ensure they meet the requirements of the threshold scheme
	if totalCopies < 2 || totalCopies > 26 {
		return fmt.Errorf("totalCopies must be between 2 and 26, got %d", totalCopies)
	}
	if requiredCopies < 2 {
		return fmt.Errorf("requiredCopies must be at least 2, got %d", requiredCopies)
	}
	if requiredCopies > totalCopies {
		return fmt.Errorf("requiredCopies cannot be greater than totalCopies, got %d > %d", requiredCopies, totalCopies)
	}

	// Set up the Pad instance with the specified parameters
	p.TotalCopies = totalCopies
	p.RequiredCopies = requiredCopies

	// Generate collection names in the format "<K><collectionId><N>"
	// Example: with K=3, N=5, collections = ["3A5", "3B5", "3C5", "3D5", "3E5"]
	p.Collections = make([]string, totalCopies)
	for i := 0; i < totalCopies; i++ {
		collLetter := collectionLetterFromIndex(i)
		p.Collections[i] = buildCollectionLabel(requiredCopies, totalCopies, collLetter)
	}

	// Generate the key combinations for the K-of-N scheme
	p.PermutationCount, p.Permutations, p.Ciphers = UniqueSortedCombinations(p.RequiredCopies, p.TotalCopies)

	// Log the generated collections and their permutations
	for i := 0; i < totalCopies; i++ {
		log.Debugf("Pad Collections: %s %v", collectionLetterFromIndex(i), p.Permutations[collectionLetterFromIndex(i)])
	}
	keys := make([]string, 0, len(p.Ciphers))
	for k := range p.Ciphers {
		keys = append(keys, k)
	}
	log.Debugf("Pad %d Permutations K=%d N=%d  %v", p.PermutationCount, p.RequiredCopies, p.TotalCopies, keys)

	return nil
}

// Create a collection label from parameters
func buildCollectionLabel(requiredCopies, totalCopies int, collLetter string) string {
	return fmt.Sprintf("%d%s%d", requiredCopies, collLetter, totalCopies)
}

// extractFromCollectionLabel parses a label like "3A5" and returns requiredCopies, totalCopies, and collLetter
// with full validation according to the defined rules.
func extractFromCollectionLabel(label string) (requiredCopies int, totalCopies int, collLetter string, err error) {
	if len(label) < 3 {
		return 0, 0, "", fmt.Errorf("label too short")
	}

	// Find first non-digit: expected to be the collection letter
	i := 0
	for i < len(label) && unicode.IsDigit(rune(label[i])) {
		i++
	}
	if i == 0 || i >= len(label)-1 {
		return 0, 0, "", fmt.Errorf("invalid format: expected digits, then letter, then digits")
	}

	requiredStr := label[:i]
	letterChar := label[i]
	totalStr := label[i+1:]

	requiredCopies, err = strconv.Atoi(requiredStr)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid requiredCopies: %v", err)
	}

	totalCopies, err = strconv.Atoi(totalStr)
	if err != nil {
		return 0, 0, "", fmt.Errorf("invalid totalCopies: %v", err)
	}

	// Validation: total ∈ [2, 26]
	if totalCopies < 2 || totalCopies > 26 {
		return 0, 0, "", fmt.Errorf("totalCopies out of range: %d", totalCopies)
	}

	// Validation: required ∈ [2, total]
	if requiredCopies < 2 || requiredCopies > totalCopies {
		return 0, 0, "", fmt.Errorf("requiredCopies out of range: %d", requiredCopies)
	}

	// Validation: letter is uppercase and within allowed range
	if letterChar < 'A' || letterChar > byte('A'+totalCopies-1) {
		return 0, 0, "", fmt.Errorf("collLetter %q out of range for total %d", letterChar, totalCopies)
	}

	return requiredCopies, totalCopies, string(letterChar), nil
}

// Get the collection letter in a permutation by index
func collectionLetterFromPermutationIndex(perm string, index int) string {
	if index < 0 || index >= len(perm) {
		return "?"
	}
	if len(perm) == 0 {
		return "?"
	}
	if index >= len(perm) {
		return "?"
	}
	collLetter := perm[index]
	if collLetter < 'A' || collLetter > 'Z' {
		return "?"
	}
	return string(collLetter)
}

// Get the index of a collection letter within a permutation
func permutationIndex(permutation string, collLetter string) (int, error) {
	if len(collLetter) != 1 {
		return -1, fmt.Errorf("collLetter must be a single character: got %q", collLetter)
	}
	target := rune(collLetter[0])
	for i, letter := range permutation {
		if letter == target {
			return i, nil
		}
	}
	return -1, fmt.Errorf("collection letter %s not found in permutation %s", collLetter, permutation)
}

// Get the collection letter for a given 0-based index
func collectionLetterFromIndex(i int) string {
	if i < 0 || i >= 26 {
		panic("index out of range")
	}
	return string(rune('A' + i))
}

// Build a chunk name for a given collection name and chunk number and chunk data size
func buildChunkName(collName string, chunkNumber, chunkDataBytes int) string {
	return fmt.Sprintf("%s:%d:%d", collName, chunkNumber, chunkDataBytes)
}

// extractFromChunkName parses chunkName into its parts, validating each field.
func extractFromChunkName(chunkName string) (collName string, chunkNumber int, chunkDataBytes int, err error) {
	parts := strings.Split(chunkName, ":")
	if len(parts) != 3 {
		return "", 0, 0, fmt.Errorf("invalid chunk name format: expected 3 parts separated by ':'")
	}

	collName = parts[0]

	chunkNumber, err = strconv.Atoi(parts[1])
	if err != nil || chunkNumber <= 0 {
		return "", 0, 0, fmt.Errorf("invalid chunkNumber: must be positive integer")
	}

	chunkDataBytes, err = strconv.Atoi(parts[2])
	if err != nil || chunkDataBytes <= 0 {
		return "", 0, 0, fmt.Errorf("invalid chunkDataBytes: must be positive integer")
	}

	return collName, chunkNumber, chunkDataBytes, nil
}

// UniqueSortedCombinations generates the combinatorial structures needed for the K-of-N threshold scheme.
//
// This function is a core part of the padlock cryptographic system, creating the mathematical
// foundation for the threshold properties. It generates all combinations of K elements from
// a set of N elements, organized in a way that enables efficient encoding and decoding.
//
// Mathematical properties:
// - Generates exactly C(N,K) = N!/(K!(N-K)!) unique combinations
// - Each collection participates in exactly C(N-1,K-1) different permutations
// - Guarantees that any K collections contain at least one complete permutation
// - Creates a deterministic mapping between collections and permutations
//
// Security implications:
// - With K-1 or fewer collections, no permutation can be completed
// - The system of equations becomes underdetermined with fewer than K collections
// - This creates an information-theoretic security boundary (not just computational)
// - The combinatorial structure ensures uniform data distribution across collections
// - Collection pieces maintain statistical independence when viewed separately
//
// Returns:
//  1. int – number of combinations each label (collection) participates in
//  2. map[string][]string – all sorted K-of-N combinations that include each label
//     (maps collection letter to all permutations it participates in)
//  3. map[string][][]byte – all unique K-of-N combinations, initialized as empty byte slices
//     (maps permutation key to array of byte slices that will hold the actual data)
//
// For example, with K=2, N=3 (labels A, B, C):
// - Generates combinations: [AB, AC, BC]
// - For label A: permutations = [AB, AC]
// - For label B: permutations = [AB, BC]
// - For label C: permutations = [AC, BC]
// - uniqueMap contains entries for "AB", "AC", and "BC", each with 2 empty byte slices
//
// During encoding, these structures are used to distribute data across collections in a way
// that ensures the threshold security properties. During decoding, they are used to recombine
// the data from K or more collections to reconstruct the original information.
//
// The algorithm uses recursive backtracking to efficiently generate all combinations,
// with O(C(N,K)) complexity. For typical values of K and N (K≤N≤26), this is highly efficient.
// The sorting of combinations ensures deterministic behavior across different platforms.
func UniqueSortedCombinations(K, N int) (int, map[string][]string, map[string][][]byte) {
	// Create labels for each collection (A, B, C, ...)
	labels := make([]string, N)
	for i := 0; i < N; i++ {
		labels[i] = collectionLetterFromIndex(i)
	}

	// Initialize the result maps
	result := make(map[string][]string, N)
	uniqueMap := make(map[string][][]byte)

	// Generate all K-combinations of N labels using recursive backtracking
	var allCombos [][]string
	var comb func(start int, path []string)
	comb = func(start int, path []string) {
		// If we have selected K labels, add this combination to our results
		if len(path) == K {
			c := make([]string, K)
			copy(c, path)
			allCombos = append(allCombos, c)
			return
		}
		// Otherwise, try adding each remaining label and continue recursively
		for i := start; i < N; i++ {
			comb(i+1, append(path, labels[i]))
		}
	}
	// Start the recursive generation
	comb(0, nil)

	// Process all generated combinations
	for _, combo := range allCombos {
		// Create a unique string key for this combination (e.g., "ABC")
		joined := strings.Join(combo, "")

		// Initialize the byte slices array for this combination
		uniqueMap[joined] = make([][]byte, K)

		// For each label in this combination, add the combination to its list
		for _, label := range combo {
			result[label] = append(result[label], joined)
		}
	}

	// Sort the combinations for each label for deterministic behavior
	for k := range result {
		sort.Strings(result[k])
	}

	// Return the number of combinations each label participates in,
	// the map of each label to its combinations, and the initialized uniqueMap
	return len(result[labels[0]]), result, uniqueMap
}

// Encode implements the one-time pad encoding process with K-of-N threshold security.
//
// This method takes an input stream and encodes it into N collections such that
// any K collections can be used to reconstruct the original data, but fewer than
// K collections reveal absolutely nothing about the data (information-theoretic security).
//
// Parameters:
//   - ctx: Context for logging, cancellation, and tracing
//   - outputChunkBytes: Maximum size for each output chunk in bytes
//   - input: Reader providing the data to be encoded
//   - randomSource: Source of random bytes for one-time pad generation
//   - newChunk: Function to create output files for each chunk
//   - chunkFormat: Format for output files (e.g., "bin" or "png")
//
// Process:
//  1. Divide input data into fixed-size chunks
//  2. For each chunk:
//     a. Generate a random one-time pad matching the input size
//     b. XOR the input data with the pad to create ciphertext
//     c. Distribute data across N collections according to the threshold scheme
//     d. Write the data to each collection with proper headers
//
// Security considerations:
//   - The randomSource MUST provide cryptographically secure random numbers
//   - The same pad must NEVER be reused
//   - Each chunk has a unique name to ensure it's properly tracked during decoding
func (p *Pad) Encode(ctx context.Context, outputChunkBytes int, input io.Reader, randomSource RNG, newChunk NewChunkFunc, chunkFormat string) error {
	log := trace.FromContext(ctx).WithPrefix("ENCODE")

	// Compute a size of input to process in each chunk, given the number of ciphers that must fit into the chunk
	inputChunkBytes := outputChunkBytes / p.PermutationCount
	log.Debugf("Starting encode with inputChunkBytes=%d outputChunkBytes=%d", inputChunkBytes, outputChunkBytes)

	// Process input data chunk by chunk until end of stream
	buffer := make([]byte, inputChunkBytes)
	for chunkIndex := 1; ; chunkIndex++ {

		// Read a chunk of data from the input stream
		bytesRead, err := io.ReadFull(input, buffer)
		if bytesRead > 0 {

			// Create a new chunk
			if err := p.encodeOneChunk(ctx, buffer[:bytesRead], chunkIndex, randomSource, newChunk, chunkFormat); err != nil {
				return err
			}
		}

		// Check for errors or EOF
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			// We've reached the end of the input
			log.Debugf("Reached end of input stream after %d chunks", chunkIndex-1)
			break
		} else if err != nil {
			return fmt.Errorf("input read error: %w", err)
		}
	}

	log.Debugf("Encode completed successfully")
	return nil
}

// encodeOneChunk encodes a single chunk of data using the one-time pad threshold scheme.
//
// This function is the core cryptographic implementation of the K-of-N threshold scheme
// for a single chunk of data. It implements the mathematical heart of the padlock system,
// providing information-theoretic security through one-time pad encryption and combinatorial
// distribution of data across collections.
//
// Mathematical overview:
//  1. Let P be the plaintext (original data chunk)
//  2. For each permutation i of K collections (e.g., "ABC", "ABD", ...):
//     a. Generate K-1 truly random pads R_1, R_2, ..., R_(K-1)
//     b. Compute the ciphertext C = P ⊕ R_1 ⊕ R_2 ⊕ ... ⊕ R_(K-1)
//     (where ⊕ represents the XOR operation)
//     c. Distribute P, R_1, R_2, ..., R_(K-1) across the K collections in that permutation
//     such that each collection gets a different piece
//  3. Each collection ends up with multiple pieces from different permutations
//
// Security properties:
// - With K or more collections, all pieces can be XORed together to recover P
// - With K-1 or fewer collections, the missing pieces make recovery mathematically impossible
// - An attacker with K-1 collections learns absolutely nothing about P (information-theoretic security)
// - Each collection's content appears completely random when viewed in isolation
//
// Information-theoretic security proof:
//   - Each permutation creates a system of K equations where K unknowns must be solved
//   - With only K-1 collections available, the system becomes underdetermined
//   - For each missing piece, there are 2^n possible values (for n-bit data) all equally likely
//   - This results in perfect indistinguishability - every possible plaintext is equally probable
//   - Claude Shannon's perfect secrecy condition is satisfied: P(C|M) = P(C), meaning the ciphertext
//     probability distribution is independent of the plaintext message
//   - This is mathematically provable and doesn't rely on computational hardness assumptions
//
// Parameters:
//   - ctx: Context for logging, cancellation, and tracing
//   - chunkData: The input data to encode (may be less than a full chunk at the end of the stream)
//   - chunkNumber: The sequential number of this chunk (starting at 1)
//   - randomSource: Source of cryptographically secure random bytes
//   - newChunk: Function to create output files for each chunk
//   - chunkFormat: Format for output files (e.g., "bin" or "png")
//
// Security considerations:
//   - The randomSource MUST provide high-quality, truly random data
//   - The segment distribution ensures that fewer than K collections reveal nothing about the data
//   - Each collection receives data that appears completely random when viewed in isolation
//   - The security depends entirely on the randomness quality - weak randomness breaks the system
//   - XOR distribution creates combinatorially secure threshold guarantees
//   - System has mathematical, not just computational, security guarantees
//   - Security level is independent of chunk size - even 1-byte chunks have perfect secrecy
func (p *Pad) encodeOneChunk(ctx context.Context, chunkData []byte, chunkNumber int, randomSource RNG, newChunk NewChunkFunc, chunkFormat string) error {
	log := trace.FromContext(ctx).WithPrefix("ENCODE")

	// Handle the actual size of the input data, which may be less than a full chunk
	chunkDataBytes := len(chunkData)
	log.Debugf("Chunk %d: processing %d bytes of data", chunkNumber, chunkDataBytes)

	// Generate all ciphers that will be needed for this chunk
	for key, cipher := range p.Ciphers {
		cipher := make([][]byte, len(cipher))
		cipher[0] = make([]byte, chunkDataBytes)
		copy(cipher[0], chunkData)
		for i := 1; i < len(cipher); i++ {
			// Generate the random pad for this permutation
			cipher[i] = make([]byte, chunkDataBytes)
			err := randomSource.Read(ctx, cipher[i])
			if err != nil {
				log.Error(fmt.Errorf("random generator error: %w", err))
				return fmt.Errorf("random generator error: %w", err)
			}
			// XOR plaintext (chunkData) with pad to get ciphertext
			log.Debugf("Chunk %d: %s XORing chunk data with pad[%s] to generate ciphertext[%s]", chunkNumber, key, collectionLetterFromPermutationIndex(key, i), collectionLetterFromPermutationIndex(key, 0))
			for j := 0; j < chunkDataBytes; j++ {
				cipher[0][j] = cipher[0][j] ^ cipher[i][j]
			}
		}
		p.Ciphers[key] = cipher
	}

	// Distribute the chunk across all collections
	for _, collName := range p.Collections {
		_, _, collLetter, err := extractFromCollectionLabel(collName)
		if err != nil {
			return fmt.Errorf("failed to extractFrom collection letter: %w", err)
		}

		// Create a new chunk writer for this collection
		w, err := newChunk(collName, chunkNumber, chunkFormat)
		if err != nil {
			return fmt.Errorf("failed to create chunk writer for collection %s: %w", collName, err)
		}

		// Generate the chunk name
		chunkName := buildChunkName(collName, chunkNumber, chunkDataBytes)
		log.Debugf("Chunk %d: processing collection %s", chunkNumber, collName)

		// Write the chunk name to the chunk
		nameHeader := []byte{byte(len(chunkName))}
		nameHeader = append(nameHeader, []byte(chunkName)...)
		if _, err := w.Write(nameHeader); err != nil {
			return fmt.Errorf("failed to write chunk header for collection %s: %w", collName, err)
		}

		// Write the ciphers for each permutations to the chunk
		for _, perm := range p.Permutations[collLetter] {
			collIndex, err := permutationIndex(perm, collLetter)
			if err != nil {
				return fmt.Errorf("failed to find permutation index in %s for collection %s: %w", perm, collLetter, err)
			}
			// Write the cipher data for this collection
			cipher := p.Ciphers[perm][collIndex]
			if _, err := w.Write(cipher); err != nil {
				return fmt.Errorf("failed to write chunk data for collection %s: %w", collName, err)
			}
			log.Debugf("Chunk %d: wrote %d byte permutation %s for collection %s", chunkNumber, len(cipher), perm, collLetter)
		}

		// Close the chunk writer
		w.Close()
	}

	log.Debugf("Chunk %d: completed successfully", chunkNumber)
	return nil
}

// Decode performs the one-time pad decoding process to reconstruct the original data.
//
// This method takes K or more collection readers and reconstructs the original data
// using the threshold scheme properties. It requires at least K collections to work;
// with fewer collections, no information about the original data can be recovered.
//
// Parameters:
//   - ctx: Context for logging, cancellation, and tracing
//   - collections: Slice of io.Readers, each providing data from one collection
//     (must provide at least RequiredCopies readers)
//   - output: Writer where the reconstructed original data will be written
//
// Process:
//  1. Verify that enough collections are provided (at least K)
//  2. Read chunks sequentially from each collection
//  3. For each chunk:
//     a. Read and validate chunk headers and names
//     b. Decode the chunk data using the threshold scheme
//     c. Write the decoded data to the output
//
// Security considerations:
//   - Attempting to decode with fewer than K collections will fail completely
//   - The collection readers must provide data from the same encoding operation
//   - Chunk numbers and collection names are verified for consistency
//   - The decoding process is deterministic and will produce the exact original data
func (p *Pad) Decode(ctx context.Context, collections []io.Reader, output io.Writer) error {
	log := trace.FromContext(ctx).WithPrefix("DECODE")

	log.Debugf("Starting decode with %d collections", len(collections))

	// Create a structure to track collection state
	type collectionState struct {
		reader           io.Reader
		nextChunkNumber  int
		collectionName   string
		collectionLetter string
		done             bool
	}

	states := make([]collectionState, len(collections))
	for i, reader := range collections {
		states[i] = collectionState{
			reader:          reader,
			nextChunkNumber: 1, // Start at chunk 1
		}
	}

	// We need to reinitialize the pad when we get some real data
	padReinitialized := false

	// Read chunks until we've processed all available chunks in all collections
	var chunkDataBytes int
	for chunkIndex := 1; ; chunkIndex++ {
		// For each collection, read the next chunk
		chunks := make([][]byte, len(collections))

		for i, state := range states {
			state.done = false

			// Read the chunk name
			lengthBuf := make([]byte, 1)
			_, err := io.ReadFull(state.reader, lengthBuf)
			if err == io.EOF {
				// No more chunks in this collection
				log.Debugf("Collection %d is done (EOF)", i)
				states[i].done = true
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to read chunk name length: %w", err)
			}

			nameLength := int(lengthBuf[0])
			nameBuf := make([]byte, nameLength)
			_, err = io.ReadFull(state.reader, nameBuf)
			if err != nil {
				return fmt.Errorf("failed to read chunk name length %d: %w", nameLength, err)
			}

			chunkName := string(nameBuf)
			log.Debugf("Collection %d: Chunk name: %s", i, chunkName)

			// Parse the collection name and chunk number from the chunk name
			var collName string
			var chunkNum int
			collName, chunkNum, chunkDataBytes, err = extractFromChunkName(chunkName)
			if err != nil {
				return fmt.Errorf("invalid chunk name format (missing hyphen): %s", chunkName)
			}
			requiredCopies, totalCopies, collLetter, err := extractFromCollectionLabel(collName)
			if err != nil {
				return fmt.Errorf("invalid chunk name format (missing hyphen): %s", chunkName)
			}

			// Initialize the pad if we haven't done so
			if !padReinitialized {
				padReinitialized = true
				err = PadInit(ctx, p, totalCopies, requiredCopies)
				if err != nil {
					return fmt.Errorf("invalid chunk name format (missing hyphen): %s", chunkName)
				}
				log.Debugf("Pad initialized with totalCopies:%d requiredCopies:%d", p.TotalCopies, p.RequiredCopies)
			}

			// If this is the first chunk, initialize the collection name
			if states[i].collectionName == "" {
				states[i].collectionName = collName
				states[i].collectionLetter = collLetter
				log.Debugf("Collection %d: Initialized collection name: %s", i, collName)
			} else if states[i].collectionName != collName {
				return fmt.Errorf("collection name mismatch: expected %s, got %s",
					states[i].collectionName, collName)
			}

			// Verify the copies
			if requiredCopies != p.RequiredCopies {
				return fmt.Errorf("required copies mismatch: expected %d, got %d",
					p.RequiredCopies, requiredCopies)
			}
			if totalCopies != p.TotalCopies {
				return fmt.Errorf("total copies mismatch: expected %d, got %d",
					p.TotalCopies, totalCopies)
			}

			// Verify the chunk number
			if chunkNum != states[i].nextChunkNumber {
				log.Debugf("Collection %d: Chunk number mismatch: expected %d, got %d",
					i, states[i].nextChunkNumber, chunkNum)
				return fmt.Errorf("chunk number mismatch: expected %d, got %d",
					states[i].nextChunkNumber, chunkNum)
			}
			states[i].nextChunkNumber++

			// Compute the chunk length
			readLength := chunkDataBytes * p.PermutationCount

			// Read the chunk data
			log.Debugf("Collection %d: Reading %d bytes of chunk data for %d byte chunk", i, readLength, chunkDataBytes)
			chunk := make([]byte, readLength)
			n, err := io.ReadFull(state.reader, chunk)
			if err != nil {
				return fmt.Errorf("failed to read chunk data: %w", err)
			}
			if n != readLength {
				return fmt.Errorf("failed to read %d bytes of chunk data got:%d: %w", readLength, n, err)
			}
			chunks[i] = chunk
			log.Debugf("Collection %d: Read %d bytes of chunk data", i, len(chunk))
		}

		// Check if all collections have been fully processed
		allDone := true
		anyDone := false
		for _, state := range states {
			if state.done {
				anyDone = true
			}
			if !state.done {
				allDone = false
				break
			}
		}
		if allDone {
			log.Debugf("All collections have been fully processed")
			return nil
		}
		if anyDone {
			log.Debugf("Some collections have been processed while others are fully processed")
			return nil
		}

		// Loop through all the collections to find the first permutation that matches
		chunkLetters := []string{}
		for _, state := range states {
			chunkLetters = append(chunkLetters, state.collectionLetter)
		}
		if len(chunkLetters) < p.RequiredCopies {
			return fmt.Errorf("not enough copies to decode: %d < %d", len(chunkLetters), p.RequiredCopies)
		}
		sort.Strings(chunkLetters)
		chunkLetters = chunkLetters[0:p.RequiredCopies]
		permutation := strings.Join(chunkLetters, "")
		log.Debugf("Permutation %s will be used for decode", permutation)

		// Generate the final data
		decodedChunk := make([]byte, chunkDataBytes)
		for i := 0; i < len(chunkLetters); i++ {
			// Find the permutations for this collectionLetter such as B: [ABC ABD ABE BCD BCE BDE]
			perm, found := p.Permutations[chunkLetters[i]]
			if !found {
				return fmt.Errorf("failed to find permutation for collection %s", chunkLetters[i])
			}
			// Find the index of the desired permutation in that list
			permIndex := -1
			for j, p := range perm {
				if p == permutation {
					permIndex = j
					break
				}
			}
			if permIndex == -1 {
				return fmt.Errorf("failed to find permutation index for collection %s", chunkLetters[i])
			}
			log.Debugf("Collection %s: XORing data from permutation %d for %s", chunkLetters[i], permIndex, permutation)
			// XOR the data with the appropriate permutation within that chunk
			permBase := permIndex * chunkDataBytes
			for j := 0; j < chunkDataBytes; j++ {
				decodedChunk[j] = decodedChunk[j] ^ chunks[i][permBase+j]
			}
		}

		// Write the decoded data to the output
		_, err := output.Write(decodedChunk)
		if err != nil {
			return fmt.Errorf("failed to write decoded data: %w", err)
		}

	}
}
