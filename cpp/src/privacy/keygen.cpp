#include <cstdint>
#include <cstdio>
#include <sodium.h>
#include <fstream>
#include "../../include/privacy/encryption.h"

/*
 * Generates a Curve25519 keypair for the sequencer.
 * Calls sodium_init() internally — safe to call even if init_privacy_layer()
 * was already called, because sodium_init() is idempotent (returns 1 if
 * already initialised, which is NOT an error).
 *
 * public_key  must point to a caller-allocated buffer of
 *             crypto_box_PUBLICKEYBYTES (32) bytes.
 * private_key must point to a caller-allocated buffer of
 *             crypto_box_SECRETKEYBYTES (32) bytes.
 *
 * Returns SO_SUCCESS on success.
 *         SO_ERR_INIT_FAILED if libsodium could not be initialised.
 *         SO_ERR_KEY_MISMATCH if keypair generation failed (should never
 *         happen in practice, but we propagate the error defensively).
 */
extern "C" int generate_sequencer_keys(uint8_t *public_key, uint8_t *private_key) {
    // sodium_init() returns  0 on first successful init,
    //                        1 if already initialised (not an error),
    //                       -1 on failure.
    // The check `< 0` correctly treats only -1 as a failure.
    if (sodium_init() < 0) {
        return SO_ERR_INIT_FAILED;
    }

    // crypto_box_keypair fills both buffers with cryptographically secure
    // random bytes using the Curve25519 algorithm.
    if (crypto_box_keypair(public_key, private_key) != 0) {
        return SO_ERR_KEY_MISMATCH;
    }

    return SO_SUCCESS;
}

/*
 * Saves a raw key to a binary file on disk.
 *
 * filepath : null-terminated path string — const char* so this function is
 *            callable from CGO without any C++ type marshalling.
 * key      : pointer to the key bytes to write.
 * len      : number of bytes to write (e.g. crypto_box_PUBLICKEYBYTES).
 *
 * Returns 1 on success, 0 on failure (file could not be opened or written).
 */
extern "C" int save_key_to_file(const char *filepath, const uint8_t *key, size_t len) {
    std::ofstream file(filepath, std::ios::binary);
    if (!file.is_open()) {
        return 0;
    }

    file.write(reinterpret_cast<const char *>(key), static_cast<std::streamsize>(len));
    if (!file.good()) {
        return 0;
    }

    file.close();
    return 1;
}

/*
 * Loads a key from a binary file into the provided buffer.
 *
 * filepath : null-terminated path string — const char* for CGO compatibility.
 * buffer   : caller-allocated buffer to read the key bytes into.
 * len      : exact number of bytes expected (e.g. crypto_box_SECRETKEYBYTES).
 *            If the file contains fewer bytes than len, this returns 0.
 *
 * Returns 1 on success, 0 on failure (file not found, too short, read error).
 */
extern "C" int load_key_from_file(const char *filepath, uint8_t *buffer, size_t len) {
    std::ifstream file(filepath, std::ios::binary);
    if (!file.is_open()) {
        return 0;
    }

    file.read(reinterpret_cast<char *>(buffer), static_cast<std::streamsize>(len));

    // gcount() returns the number of bytes actually read.
    // We require an exact match — a short read means the file is truncated.
    if (file.gcount() != static_cast<std::streamsize>(len)) {
        return 0;
    }

    return 1;
}