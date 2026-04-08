#!/bin/bash
set -e

echo "🔧 Building SecureOrder Production Binaries"
echo "=========================================="

# Clean up old system files that interfere with the build
echo "📦 Removing old system files..."
if [ -d /usr/local/include/privacy ]; then
    echo "   Found /usr/local/include/privacy - needs removal to avoid conflicts"
    echo "   Please run: sudo rm -rf /usr/local/include/privacy /usr/local/lib/libprivacy.a"
    read -p "   Run with sudo now? (y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        sudo rm -rf /usr/local/include/privacy /usr/local/lib/libprivacy.a
        echo "   ✅ Cleaned up"
    else
        echo "   ⚠️  Please remove manually and run this script again"
        exit 1
    fi
fi

# Build C++ library
echo ""
echo "🔨 Building C++ Privacy Layer..."
cd cpp/build
cmake -DCMAKE_INSTALL_PREFIX=. ..
make install
cd ../..
echo "✅ C++ built"

# Build Go binaries
echo ""
echo "🔨 Building Go Sequencer..."
export LD_LIBRARY_PATH="./cpp/build/lib:$LD_LIBRARY_PATH"
go mod tidy
go build -o bin/sequencer ./cmd/sequencer
echo "✅ Sequencer built: ./bin/sequencer"

echo ""
echo "🔨 Building Go Client..."
go build -o bin/client ./cmd/client
echo "✅ Client built: ./bin/client"

echo ""
echo "=========================================="
echo "✅ Build Complete!"
echo ""
echo "Next steps:"
echo "  1. Start server (Terminal 1):"
echo "     export LD_LIBRARY_PATH=\"./cpp/build/lib:\$LD_LIBRARY_PATH\""
echo "     ./bin/sequencer"
echo ""
echo "  2. Send transactions (Terminal 2+):"
echo "     export LD_LIBRARY_PATH=\"./cpp/build/lib:\$LD_LIBRARY_PATH\""
echo "     ./bin/client"
echo ""
echo "  3. Send 10 concurrent clients (Terminal 3):"
echo "     for i in {1..10}; do"
echo "       (export LD_LIBRARY_PATH=\"./cpp/build/lib:\$LD_LIBRARY_PATH\" && ./bin/client) &"
echo "     done"
echo "     wait"
