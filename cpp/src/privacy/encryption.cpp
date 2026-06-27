/*
 * encryption.cpp — core C-linkage encryption/decryption functions.
 *
 * All functions are declared extern "C" so they can be called from CGO
 * without name-mangling. The actual crypto is entirely provided by libsodium;
 * this file is a thin adapter layer that:
 *   - Enforces size invariants before calling libsodium.
 *   - Maps libsodium return codes to the SO_* error constants.
 *   - Fills the DecryptedTx output struct so the Go layer can copy results.
 */
#include <cstddef>
#include <cstdint>
#include <algorithm>
#include <atomic>
#include <thread>
#include <vector>
#include <sodium.h>
#include "../../include/privacy/encryption.h"

// ── Lifecycle ────────────────────────────────────────────────────────────────

/*
 * init_privacy_layer — must be called once at sequencer startup.
 *
 * sodium_init() seeds the platform CSPRNG and performs CPU feature detection
 * (picks the fastest AVX2/SSE2/NEON code path for XSalsa20 and Poly1305).
 *
 * Return values from sodium_init():
 *   0  — first successful initialisation
 *   1  — already initialised (safe to call again, not an error)
 *  -1  — initialisation failed (no entropy source, unsupported platform)
 *
 * We treat 0 and 1 as SO_SUCCESS and only -1 as a failure.
 */
extern "C" int init_privacy_layer(void) {
    int ret = sodium_init();
    if (ret < 0) {
        return SO_ERR_INIT_FAILED;
    }
    return SO_SUCCESS;
}

// ── Client Side ──────────────────────────────────────────────────────────────

/*
 * seal_transaction — encrypts a transaction for the sequencer.
 *
 * Uses NaCl crypto_box_seal (anonymous sealed box):
 *   1. Generates a random ephemeral Curve25519 keypair.
 *   2. Computes a shared secret from the ephemeral private key + seq_pub_key.
 *   3. Encrypts plaintext with XSalsa20 using the shared secret.
 *   4. Appends a Poly1305 MAC tag for integrity verification.
 *   5. Prepends the ephemeral public key (32 bytes) to the ciphertext.
 *   6. Discards the ephemeral private key — sender is permanently anonymous.
 *
 * Output layout:
 *   [ 32-byte ephemeral pubkey | encrypted plaintext | 16-byte MAC ]
 *   total = len + crypto_box_SEALBYTES (48)
 *
 * Caller must pre-allocate ciphertext to at least (len + crypto_box_SEALBYTES).
 *
 * Returns SO_SUCCESS or SO_ERR_BAD_CIPHER.
 */
extern "C" int seal_transaction(
    const uint8_t *plaintext,
    size_t         len,
    const uint8_t *seq_pub_key,
    uint8_t       *ciphertext)
{
    if (crypto_box_seal(ciphertext, plaintext, len, seq_pub_key) != 0) {
        return SO_ERR_BAD_CIPHER;
    }
    return SO_SUCCESS;
}

// ── Sequencer Side — Single (Baseline) ───────────────────────────────────────

/*
 * decrypt_single_tx — decrypts one sealed ciphertext.
 *
 * Reverses seal_transaction:
 *   1. Reads the 32-byte ephemeral pubkey from the front of the ciphertext.
 *   2. Re-derives the shared secret using (ephemeral_pubkey, priv_key).
 *   3. Verifies the Poly1305 MAC — returns SO_ERR_KEY_MISMATCH if invalid
 *      (wrong key or tampered ciphertext; libsodium does not distinguish).
 *   4. Decrypts with XSalsa20 and writes the plaintext to output->plaintext.
 *
 * Both pub_key and priv_key are required: pub_key is used to complete the
 * DH key-agreement step alongside the ephemeral pubkey from the ciphertext.
 *
 * Preconditions:
 *   - input->length >= crypto_box_SEALBYTES (48) — enforced by the guard below.
 *   - output->plaintext pre-allocated to (input->length - crypto_box_SEALBYTES).
 *
 * On success: output->length = plaintext length, output->status = SO_SUCCESS.
 * On failure: output->length = 0,               output->status = error code.
 *
 * Returns the same value as output->status.
 */
extern "C" int decrypt_single_tx(
    const EncryptedTx *input,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *output)
{
    // Guard: a ciphertext shorter than the seal overhead cannot possibly be valid.
    // Without this check, libsodium would read out-of-bounds on the input buffer.
    if (input->length < crypto_box_SEALBYTES) {
        output->status = SO_ERR_BAD_CIPHER;
        output->length = 0;
        return SO_ERR_BAD_CIPHER;
    }

    size_t plaintext_len = input->length - crypto_box_SEALBYTES;

    int res = crypto_box_seal_open(
        output->plaintext,   // destination: caller-allocated buffer
        input->ciphertext,   // source: full sealed box (ephemeral pk + ct + mac)
        input->length,       // total source length
        pub_key,             // sequencer permanent public key
        priv_key             // sequencer permanent private key
    );

    if (res != 0) {
        // libsodium returns -1 for any verification failure.
        // We map this to SO_ERR_KEY_MISMATCH (wrong key or tampered data).
        output->status = SO_ERR_KEY_MISMATCH;
        output->length = 0;
        return SO_ERR_KEY_MISMATCH;
    }

    output->length = plaintext_len;
    output->status = SO_SUCCESS;
    return SO_SUCCESS;
}

// ── Sequencer Side — Batch (Optimised) ───────────────────────────────────────

/*
 * decrypt_batch_tx — decrypts a batch of transactions in parallel.
 *
 * Design decisions:
 *
 * Parallelism: spawns raw std::thread workers (one per chunk). Thread count is
 * capped at batch_size so we never spin up idle threads when the batch is
 * smaller than hardware_concurrency. This function uses raw threads rather than
 * the persistent ThreadPool used by BatchDecryptor — it is the simpler, stateless
 * version used when callers want to avoid managing a BatchDecryptor instance.
 *
 * Per-item independence: each item's result is set in outputs[i] independently.
 * A bad ciphertext sets that item's status to SO_ERR_BAD_CIPHER and sets
 * any_failure, but does NOT abort processing of other items in the same chunk.
 *
 * Atomic failure flag: any_failure is written with relaxed ordering because we
 * only need eventual visibility (we read it after join() of all threads), and
 * the flag is monotonic (only ever transitions SUCCESS → BAD_CIPHER).
 *
 * Chunk distribution: ceiling division ensures the last thread picks up any
 * remainder. e.g. 10 items / 3 threads → chunks of 4, 4, 2.
 *
 * Returns SO_SUCCESS only if ALL items succeeded.
 *         SO_ERR_BAD_CIPHER if one or more items failed (check per-item status).
 */
extern "C" int decrypt_batch_tx(
    const EncryptedTx *inputs,
    size_t             batch_size,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *outputs)
{
    if (batch_size == 0) return SO_SUCCESS;

    // Cap thread count so we never create more threads than there is work.
    size_t num_threads = std::thread::hardware_concurrency();
    if (num_threads == 0) num_threads = 2; // fallback on platforms where detection fails
    num_threads = std::min(num_threads, batch_size);

    std::vector<std::thread> workers;
    workers.reserve(num_threads);

    // Shared failure flag — only written atomically, never cleared.
    // Read once after all threads join.
    std::atomic<int> any_failure{SO_SUCCESS};

    // Ceiling division: (batch_size + num_threads - 1) / num_threads
    size_t chunk_size = (batch_size + num_threads - 1) / num_threads;

    // Lambda captures all arrays by reference (safe: arrays outlive all threads).
    // pub_key and priv_key are read-only after sequencer startup.
    auto worker_task = [&](size_t start, size_t end) {
        for (size_t i = start; i < end; i++) {

            // Guard: reject structurally invalid ciphertexts without calling libsodium.
            if (inputs[i].length < crypto_box_SEALBYTES) {
                outputs[i].status = SO_ERR_BAD_CIPHER;
                outputs[i].length = 0;
                any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
                continue; // do NOT abort — continue processing the rest of the chunk
            }

            size_t plaintext_len = inputs[i].length - crypto_box_SEALBYTES;

            int res = crypto_box_seal_open(
                outputs[i].plaintext,
                inputs[i].ciphertext,
                inputs[i].length,
                pub_key,
                priv_key
            );

            if (res != 0) {
                outputs[i].status = SO_ERR_BAD_CIPHER;
                outputs[i].length = 0;
                any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
            } else {
                outputs[i].length         = plaintext_len;
                outputs[i].original_index = static_cast<uint32_t>(i);
                outputs[i].status         = SO_SUCCESS;
            }
        }
    };

    // Spawn one thread per chunk.
    for (size_t t = 0; t < num_threads; t++) {
        size_t start = t * chunk_size;
        size_t end   = std::min(start + chunk_size, batch_size);
        workers.emplace_back(worker_task, start, end);
    }

    // Join all threads before reading any_failure. join() provides the
    // happens-before guarantee that makes relaxed atomic writes visible here.
    for (auto &w : workers) {
        w.join();
    }

    return any_failure.load();
}
