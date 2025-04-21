#!/bin/bash
# Script to build Padlock for multiple platforms

set -e

# Create bin directory structure
mkdir -p bin/macos-arm64
mkdir -p bin/macos-amd64
mkdir -p bin/windows-arm64
mkdir -p bin/windows-amd64
mkdir -p bin/linux-arm64
mkdir -p bin/linux-amd64
mkdir -p bin/linux-armv7

# Version from git tag or commit hash if no tag
VERSION=$(git describe --tags --always)

# Build for each platform
echo "Building Padlock v$VERSION for multiple platforms..."

# macOS (Darwin) ARM64
echo "Building for macOS ARM64..."
GOOS=darwin GOARCH=arm64 go build -o bin/macos-arm64/padlock cmd/padlock/main.go
(cd bin/macos-arm64 && shasum -a 256 padlock > padlock.sha256.txt)

# macOS (Darwin) AMD64
echo "Building for macOS AMD64..."
GOOS=darwin GOARCH=amd64 go build -o bin/macos-amd64/padlock cmd/padlock/main.go
(cd bin/macos-amd64 && shasum -a 256 padlock > padlock.sha256.txt)

# Windows ARM64
echo "Building for Windows ARM64..."
GOOS=windows GOARCH=arm64 go build -o bin/windows-arm64/padlock.exe cmd/padlock/main.go
(cd bin/windows-arm64 && sha256sum padlock.exe > padlock.exe.sha256.txt)

# Windows AMD64
echo "Building for Windows AMD64..."
GOOS=windows GOARCH=amd64 go build -o bin/windows-amd64/padlock.exe cmd/padlock/main.go
(cd bin/windows-amd64 && sha256sum padlock.exe > padlock.exe.sha256.txt)

# Linux ARM64
echo "Building for Linux ARM64..."
GOOS=linux GOARCH=arm64 go build -o bin/linux-arm64/padlock cmd/padlock/main.go
(cd bin/linux-arm64 && sha256sum padlock > padlock.sha256.txt)

# Linux AMD64
echo "Building for Linux AMD64..."
GOOS=linux GOARCH=amd64 go build -o bin/linux-amd64/padlock cmd/padlock/main.go
(cd bin/linux-amd64 && sha256sum padlock > padlock.sha256.txt)

# Linux ARMv7
echo "Building for Linux ARMv7..."
GOOS=linux GOARCH=arm GOARM=7 go build -o bin/linux-armv7/padlock cmd/padlock/main.go
(cd bin/linux-armv7 && sha256sum padlock > padlock.sha256.txt)

echo "Build complete!"
echo "Binaries available in the bin/ directory"

# Make the script executable
chmod +x bin/*/padlock 2>/dev/null || true