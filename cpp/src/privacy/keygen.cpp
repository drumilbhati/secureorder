/*
 * keygen.cpp — sequencer key generation and key file I/O.
 *
 * All functions use extern "C" linkage so they can be called from CGO without
 * C++ name mangling. The key format is raw binary (no PEM, no base64) so the
 * files are exactly 32 bytes each and can be read with a single fread.
 */
#include <cstdint>
#include <cstdio>
#include <sodium.h>
#include <fstream>
#include "../../include/privacy/encryption.h"

/*
 * generate_sequencer_keys — create a fresh Curve25519 keypair.
 *
 * Curve25519 produces a 256-bit (32-byte) Diffie-Hellman keypair:
 *   public_key  — can be shared with clients; used as the sealing key.
 *   private_key — must be kept secret; required to open sealed boxes.
 *
 * sodium_init() is called defensively here. The Go Init() function calls it
 * first, but generate_sequencer_keys may be called independently in tests
 * or from other contexts, so we re-initialise if needed. sodium_init() is
 * idempotent: it returns 1 (not -1) if already initialised, which is fine.
 *
 * crypto_box_keypair fills both buffers with cryptographically secure random
 * bytes from the platform CSPRNG (getrandom / /dev/urandom / BCryptGenRandom).
 *
 * Caller must pre-allocate:
 *   public_key  to at least crypto_box_PUBLICKEYBYTES (32) bytes.
 *   private_key to at least crypto_box_SECRETKEYBYTES (32) bytes.
 *
 * Returns SO_SUCCESS, SO_ERR_INIT_FAILED, or SO_ERR_KEY_MISMATCH.
 */
extern "C" int generate_sequencer_keys(uint8_t *public_key, uint8_t *private_key) {
    // sodium_init() returns 0 (first init), 1 (already inited), or -1 (failure).
    // Only -1 is an error.
    if (sodium_init() < 0) {
        return SO_ERR_INIT_FAILED;
    }

    // Generate a random Curve25519 keypair. Returns 0 on success, -1 on failure.
    // Failure should never occur in practice (would require a broken CSPRNG).
    if (crypto_box_keypair(public_key, private_key) != 0) {
        return SO_ERR_KEY_MISMATCH;
    }

    return SO_SUCCESS;
}

/*
 * save_key_to_file — write a raw key to a binary file.
 *
 * Opens filepath for writing in binary mode and writes exactly len bytes.
 * The file is created if it does not exist and truncated if it does.
 *
 * Parameters:
 *   filepath  — null-terminated path string (const char* for CGO compatibility;
 *               std::string is not safe to pass across the C/C++ ABI boundary).
 *   key       — pointer to the key bytes to write.
 *   len       — number of bytes to write (32 for pub or secret key).
 *
 * Returns 1 on success, 0 on failure (open error or write error).
 *
 * Note: no locking — the caller is responsible for ensuring only one process
 * writes to the key file at a time (the Go startup sequence is single-threaded
 * during key setup).
 */
extern "C" int save_key_to_file(const char *filepath, const uint8_t *key, size_t len) {
    std::ofstream file(filepath, std::ios::binary);
    if (!file.is_open()) {
        return 0; // could not open or create the file
    }

    // reinterpret_cast: ofstream expects const char*, but key bytes are uint8_t.
    file.write(reinterpret_cast<const char *>(key), static_cast<std::streamsize>(len));
    if (!file.good()) {
        return 0; // partial write or I/O error
    }

    file.close();
    return 1;
}

/*
 * load_key_from_file — read a raw key from a binary file into buffer.
 *
 * Opens filepath for reading in binary mode and reads exactly len bytes.
 * If the file contains fewer bytes than len the read is considered a failure
 * (the key file would be truncated and unusable).
 *
 * Parameters:
 *   filepath  — null-terminated path string (const char* for CGO compatibility).
 *   buffer    — caller-allocated buffer to receive the key bytes.
 *   len       — exact number of bytes expected (32 for pub or secret key).
 *
 * Returns 1 on success, 0 on any failure (file not found, short read, I/O error).
 *
 * gcount() returns the number of bytes actually read. We require an exact match
 * so a truncated key file is rejected rather than silently padding with zeros.
 */
extern "C" int load_key_from_file(const char *filepath, uint8_t *buffer, size_t len) {
    std::ifstream file(filepath, std::ios::binary);
    if (!file.is_open()) {
        return 0; // file not found or permission denied
    }

    file.read(reinterpret_cast<char *>(buffer), static_cast<std::streamsize>(len));

    // Verify that we read the expected number of bytes.
    // A short read means the key file is truncated — reject it.
    if (file.gcount() != static_cast<std::streamsize>(len)) {
        return 0;
    }

    return 1;
}
