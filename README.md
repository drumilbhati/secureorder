# Secure-Order: A FIFO Sequencing Layer for MEV Mitigation

## Team
- Devarsh Doshi
- Dhairya Rupani  
- Drumil Bhati
- Prasham Mehta
- Vidhan Nahar

## Project Overview

Secure-Order is a modular FIFO-based sequencing layer that eliminates MEV exploitation through encrypted transaction ordering.

### Key Features
- 🔐 **Privacy Layer (C++)**: Fast encryption using libsodium
- ⏱️ **Sequencing Engine (Go)**: High-performance FIFO queue
- 🔒 **Order Commitment**: Cryptographic proof on blockchain
- 🚫 **MEV Prevention**: Bot-resistant architecture

## Architecture
```
Users → Privacy Layer (C++) → Sequencing Engine (Go) → Mock DEX
                ↓
        Order Commitment (Smart Contract)
```

## Technology Stack

- **Privacy Layer**: C++ with libsodium
- **Sequencing Engine**: Go (Golang)
- **Mock DEX**: Go
- **Smart Contracts**: Solidity

## Prerequisites

- Go 1.21 or higher
- GCC/G++ compiler (for C++)
- CMake 3.10 or higher
- libsodium library
- Make
- Docker (optional)

## Quick Start

### Installation

#### 1. Clone Repository
```bash
git clone https://github.com/YOUR-USERNAME/secure-order.git
cd secure-order
```

#### 2. Install Dependencies

**On Ubuntu/Debian:**
```bash
sudo apt update
sudo apt install -y build-essential cmake libsodium-dev
```

**On macOS:**
```bash
brew install libsodium cmake
```

#### 3. Build Project
```bash
# Build everything
make all

# Or build individually
make cpp      # Build C++ privacy layer
make go       # Build Go components
```

### Run Demo
```bash
make run-demo
```

Or manually:
```bash
./bin/demo
```

## Project Structure
```
secure-order/
├── cmd/              # Main applications (Go)
├── pkg/              # Public Go packages
├── internal/         # Private Go code
├── cpp/              # C++ privacy layer
├── contracts/        # Smart contracts
├── scripts/          # Helper scripts
├── docs/             # Documentation
└── tests/            # Integration tests
```

## Building

### Build C++ Privacy Layer
```bash
cd cpp
mkdir build && cd build
cmake ..
make
```

### Build Go Components
```bash
go build -o bin/sequencer ./cmd/sequencer
go build -o bin/demo ./cmd/demo
```

### Build All (Using Makefile)
```bash
make all
```

## Testing
```bash
# Run all tests
make test

# Run Go tests
make test-go

# Run C++ tests
make test-cpp
```

## Development

### C++ Component (Privacy Layer)

Located in `cpp/`:
- Encryption/decryption using libsodium
- Fast cryptographic operations
- Header files in `cpp/include/`
- Source files in `cpp/src/`

### Go Component (Sequencing)

Located in `cmd/` and `pkg/`:
- FIFO queue management
- Batch processing
- Merkle commitment
- DEX simulation

### Interfacing C++ and Go

We use CGO to call C++ functions from Go:
```go
// #cgo LDFLAGS: -L./cpp/build -lprivacy -lsodium
// #include "cpp/include/privacy/encryption.h"
import "C"
```

## Documentation

- [System Architecture](docs/architecture.md)
- [Algorithm Explanation](docs/algorithms.md)
- [API Reference](docs/api.md)
- [C++ API](docs/cpp-api.md)
- [Go API](docs/go-api.md)

## Performance

Expected throughput:
- Encryption: 50,000+ tx/sec (C++)
- Sequencing: 10,000+ tx/sec (Go)
- End-to-end: 5,000+ tx/sec

## References

- [Flash Boys 2.0](https://arxiv.org/abs/1904.05234)
- [libsodium Documentation](https://libsodium.gitbook.io/)
- [Go Documentation](https://go.dev/doc/)

## License

MIT License

---

**Making blockchain fair, one encrypted transaction at a time.** 🚀
