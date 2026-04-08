# Multi-stage build - use latest golang image
FROM golang:latest AS builder

RUN apt-get update && apt-get install -y \
    build-essential \
    cmake \
    libsodium-dev \
    ca-certificates && \
    rm -rf /var/cache/apt/lists/*

WORKDIR /app
COPY . .

# Fix go.mod
RUN go mod tidy

# Build C++ library
RUN cd cpp && mkdir -p build && cd build && \
    cmake .. && make install && cd ../..

# Build Go binary
RUN cd /app && \
    CGO_ENABLED=1 \
    CGO_CXXFLAGS="-std=c++17" \
    CGO_LDFLAGS="-lprivacy -lsodium -lstdc++ -L/usr/local/lib" \
    GOOS=linux \
    LD_LIBRARY_PATH=/usr/local/lib:$LD_LIBRARY_PATH \
    go build -o sequencer ./cmd/sequencer

# Runtime stage - minimal image
FROM ubuntu:22.04

RUN apt-get update && apt-get install -y \
    libsodium23 \
    ca-certificates && \
    rm -rf /var/cache/apt/lists/*

WORKDIR /app
COPY --from=builder /app/sequencer /app/sequencer
COPY --from=builder /usr/local/lib/libprivacy.a /app/lib/

# Expose gRPC port
EXPOSE 50051

# Set library path
ENV LD_LIBRARY_PATH=/app/lib

ENTRYPOINT ["/app/sequencer"]
