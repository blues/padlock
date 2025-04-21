# Padlock Overview

## Introduction

Padlock is a high-performance K-of-N threshold data encoding and decoding utility that implements a one-time-pad scheme for secure data archiving and border-crossings. It was created by Ray Ozzie as a post-quantum encryption solution.

## Key Features

- **Threshold Security:**  
  The data is split into N collections, where at least K collections (with 2 ≤ K ≤ N ≤ 26) are needed to reconstruct the original content. With fewer than K collections, no information is revealed.

- **Stream-Pipelined Processing:**  
  Both the encoding and decoding processes operate as fully streaming pipelines, processing the data chunk-by-chunk without needing to load the entire dataset into memory.

- **Information-Theoretic Security:**  
  Instead of computational cryptography, Padlock uses a one-time-pad threshold scheme based on information theory, making it resistant to quantum computing attacks.

- **Flexible Output Formats:**  
  Data chunks are stored as individual files in one of two formats:
  - **PNG Files:** Useful for steganographic storage with CRC validation
  - **Raw Binary Files (.bin):** Efficient for direct data storage

- **Comprehensive Serialization:**  
  Padlock can process entire directories, automatically serializing and optionally compressing the content before encoding.

- **ZIP Collection Support:**  
  Collections can be automatically packaged as ZIP archives for easier distribution and storage.

## Use Cases

Padlock is particularly well-suited for the following use cases:

### 1. Secure Data Archiving

Padlock provides a robust solution for long-term archival of sensitive data. By distributing collections across different storage locations, it ensures both security and redundancy.

### 2. Border Crossing Security

When traveling internationally with sensitive data, Padlock allows you to distribute the collections across different devices or cloud storage services, ensuring that no single point of compromise can reveal your data.

### 3. Post-Quantum Security

As quantum computing advances threaten traditional cryptographic methods, Padlock's information-theoretic security remains unaffected, providing future-proof protection.

### 4. Distributed Backup Systems

Organizations can implement secure backup strategies where different departments or locations hold different collections, requiring collaboration for data recovery.

## Advantages Over Traditional Encryption

Padlock offers several advantages over traditional encryption methods:

1. **Quantum Resistance:** Unlike RSA, ECC, or even some post-quantum algorithms, Padlock's security does not rely on computational hardness assumptions that might be broken by quantum computers.

2. **Perfect Secrecy:** With proper implementation, Padlock provides information-theoretic security, meaning it is mathematically provable that insufficient collections reveal absolutely nothing about the original data.

3. **Threshold Recovery:** Unlike traditional encryption where a single key provides full access, Padlock requires a minimum number of collections, allowing for more flexible security policies.

4. **No Key Management:** There are no encryption keys to manage or store securely, eliminating a common point of failure in cryptographic systems.

5. **Streaming Operation:** Padlock processes data in a streaming fashion, making it suitable for large datasets without excessive memory requirements.
