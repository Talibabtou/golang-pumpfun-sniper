#!/bin/bash

# Generate protobuf Go files for Yellowstone gRPC
echo "🔧 Generating protobuf Go files..."

# Create proto directory if it doesn't exist
mkdir -p proto

# Install protoc-gen-go and protoc-gen-go-grpc if not already installed
if ! command -v protoc-gen-go &> /dev/null; then
    echo "Installing protoc-gen-go..."
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

if ! command -v protoc-gen-go-grpc &> /dev/null; then
    echo "Installing protoc-gen-go-grpc..."
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
fi

# Generate Go files from protobuf definitions
echo "Generating Go files from proto definitions..."
protoc --go_out=. --go-grpc_out=. \
    --proto_path=proto \
    proto/geyser.proto proto/solana-storage.proto

if [ $? -eq 0 ]; then
    echo "✅ Protobuf files generated successfully"
    echo "📁 Generated files:"
    find proto -name "*.go" -type f 2>/dev/null || echo "   Files generated in proto/ directory"
else
    echo "❌ Failed to generate protobuf files"
    echo "💡 Make sure you have protoc installed:"
    echo "   - macOS: brew install protobuf"
    echo "   - Ubuntu: apt-get install protobuf-compiler"
    echo "   - Or install from: https://grpc.io/docs/protoc-installation/"
    exit 1
fi

echo "🚀 Ready to use Yellowstone gRPC streaming!"
