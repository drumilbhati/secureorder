#include <cstddef>
#include <cstdint>
#include <algorithm>
#include <atomic>
#include <thread>
#include <vector>
#include <sodium.h>
#include "../../include/privacy/encryption.h"

// Lifecycle

/*
 * Must be called once at startup before any crypto operations.
 * sodium_init() returns  0 on first init,
 *                        1 if already initialised (not an error),
 *                       -1 on failure.
 */
extern "C" int init_privacy_layer(void) {
    int ret = sodium_init();
    if (ret < 0) {
        return SO_ERR_INIT_FAILED;
    }
    // ret == 0 (first init) or ret == 1 (already initialised) — both are fine
    return SO_SUCCESS;
}

// Client Side

/*
 * Encrypts a transaction using the sequencer's public key.
 * The sender is anonymous — no sender keypair is required.
 * ciphertext must be pre-allocated to at least
 *   (len + crypto_box_SEALBYTES) bytes by the caller.
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

// Sequencer Side — Single (Baseline)

/*
 * Decrypts a single transaction using the sequencer's keypair.
 * Sets output->length = input->length - crypto_box_SEALBYTES on success.
 * Sets output->status  = SO_SUCCESS or SO_ERR_KEY_MISMATCH.
 * output->plaintext must be pre-allocated to at least
 *   (input->length - crypto_box_SEALBYTES) bytes by the caller.
 */
extern "C" int decrypt_single_tx(
    const EncryptedTx *input,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *output)
{
    // Guard: ciphertext must be at least as long as the seal overhead
    if (input->length < crypto_box_SEALBYTES) {
        output->status = SO_ERR_BAD_CIPHER;
        output->length = 0;
        return SO_ERR_BAD_CIPHER;
    }

    size_t plaintext_len = input->length - crypto_box_SEALBYTES;

    int res = crypto_box_seal_open(
        output->plaintext,
        input->ciphertext,
        input->length,
        pub_key,
        priv_key
    );

    if (res != 0) {
        output->status = SO_ERR_KEY_MISMATCH;
        output->length = 0;
        return SO_ERR_KEY_MISMATCH;
    }

    output->length = plaintext_len;
    output->status = SO_SUCCESS;
    return SO_SUCCESS;
}

// Sequencer Side — Batch (Optimised)

/*
 * Decrypts a batch of transactions in parallel using raw std::thread workers.
 * Each item's result is recorded independently in outputs[i].status —
 * a single bad ciphertext does NOT abort the rest of the batch.
 *
 * Thread count is capped at batch_size so we never spin up idle threads
 * when the batch is smaller than the number of CPU cores.
 *
 * Returns SO_SUCCESS if every item decrypted successfully,
 *         SO_ERR_BAD_CIPHER if one or more items failed.
 */
extern "C" int decrypt_batch_tx(
    const EncryptedTx *inputs,
    size_t             batch_size,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *outputs)
{
    if (batch_size == 0) return SO_SUCCESS;

    // Cap thread count to batch_size — no point spawning more threads than work
    size_t num_threads = std::thread::hardware_concurrency();
    if (num_threads == 0) num_threads = 2;
    num_threads = std::min(num_threads, batch_size);

    std::vector<std::thread> workers;
    workers.reserve(num_threads);

    // Shared flag: written atomically, only ever transitions SUCCESS -> BAD_CIPHER
    std::atomic<int> any_failure{SO_SUCCESS};

    // Ceiling division: ensures the last chunk picks up any remainder
    size_t chunk_size = (batch_size + num_threads - 1) / num_threads;

    auto worker_task = [&](size_t start, size_t end) {
        for (size_t i = start; i < end; i++) {

            // Guard: ciphertext must be at least as long as the seal overhead
            if (inputs[i].length < crypto_box_SEALBYTES) {
                outputs[i].status = SO_ERR_BAD_CIPHER;
                outputs[i].length = 0;
                any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
                continue;   // do NOT abort — process the rest of the chunk
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

    for (size_t t = 0; t < num_threads; t++) {
        size_t start = t * chunk_size;
        size_t end   = std::min(start + chunk_size, batch_size);
        workers.emplace_back(worker_task, start, end);
    }

    for (auto &w : workers) {
        w.join();
    }

    return any_failure.load();
}