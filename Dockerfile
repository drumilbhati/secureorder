# Multi-stage build - Build C++ first, then Go
FROM ubuntu:22.04 AS cpp-builder

RUN apt-get update && apt-get install -y \
    build-essential \
    cmake \
    pkg-config \
    libsodium-dev \
    ca-certificates && \
    rm -rf /var/cache/apt/lists/*

WORKDIR /app
COPY cpp/ /app/cpp/

RUN cd /app/cpp && mkdir -p build && cd build && \
    cmake .. && make install

# Go builder stage
FROM golang:latest AS go-builder

RUN apt-get update && apt-get install -y \
    build-essential \
    libsodium-dev && \
    rm -rf /var/cache/apt/lists/*

WORKDIR /app
COPY --from=cpp-builder /usr/local/lib/libprivacy.a /usr/local/lib/
COPY --from=cpp-builder /usr/local/include/privacy /usr/local/include/privacy
COPY . .

RUN go mod tidy

RUN CGO_ENABLED=1 \
    CGO_CXXFLAGS="-std=c++17 -I/usr/include/c++/11 -I/usr/local/include" \
    CGO_LDFLAGS="-L/usr/local/lib -L/usr/lib -lprivacy -lsodium -lstdc++" \
    go build -o sequencer ./cmd/sequencer

# Runtime stage - minimal image
FROM ubuntu:22.04

RUN apt-get update && apt-get install -y \
    libsodium23 \
    ca-certificates && \
    rm -rf /var/cache/apt/lists/*

WORKDIR /app
COPY --from=go-builder /app/sequencer /app/sequencer
COPY --from=cpp-builder /usr/local/lib/libprivacy.a /app/lib/

ENV LD_LIBRARY_PATH=/app/lib

EXPOSE 12345

ENTRYPOINT ["/app/sequencer"]
