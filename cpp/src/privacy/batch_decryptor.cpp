#include "../../include/privacy/batch_decryptor.h"
#include "../../include/privacy/encryption.h"
#include "../../include/privacy/thread_pool.h"
#include <algorithm>
#include <atomic>
#include <cstdint>
#include <thread>
#include <sodium.h>

BatchDecryptor::BatchDecryptor() : pool(std::thread::hardware_concurrency()) {}

/*
 * Orchestrates parallel batch decryption using the persistent ThreadPool.
 *
 * Key design decisions:
 *  - The ThreadPool is kept alive between batches (constructed once in the
 *    BatchDecryptor constructor) so there is zero thread-spawn overhead per
 *    batch call — workers are already waiting for tasks.
 *  - Thread count is capped at batch_size so we never submit empty chunks
 *    when the batch is smaller than the number of CPU cores.
 *  - Per-item errors: a single bad ciphertext sets that item's status to
 *    SO_ERR_BAD_CIPHER and continues processing the rest of the chunk.
 *    The overall return value is SO_ERR_BAD_CIPHER if ANY item failed,
 *    SO_SUCCESS only if ALL items succeeded.
 *  - outputs[j].length is always set explicitly — either to the correct
 *    plaintext length on success, or to 0 on failure.
 *  - outputs[j].original_index records the position in the ordered batch
 *    so downstream code can reassemble results without relying on order.
 */
int BatchDecryptor::decrypt_batch(
    const EncryptedTx *inputs,
    size_t             batch_size,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *outputs)
{
    if (batch_size == 0) return SO_SUCCESS;

    // Cap thread count to batch_size — no point submitting empty chunks
    size_t num_threads = std::thread::hardware_concurrency();
    if (num_threads == 0) num_threads = 2;
    num_threads = std::min(num_threads, batch_size);

    // Shared failure flag — written atomically, never cleared once set.
    // Each worker sets it independently; we read it once after wait_all().
    std::atomic<int> any_failure{SO_SUCCESS};

    // Ceiling division ensures the last chunk picks up any remainder.
    // e.g. 10 items / 3 threads → chunks of 4, 4, 2  (not 3, 3, 4)
    size_t chunk_size = (batch_size + num_threads - 1) / num_threads;

    for (size_t t = 0; t < num_threads; t++) {
        size_t start = t * chunk_size;
        size_t end   = std::min(start + chunk_size, batch_size);

        // Safety: should never happen given the cap above, but guard anyway
        if (start >= batch_size) break;

        // Capture start/end by value, everything else by reference.
        // pub_key and priv_key are read-only after sequencer startup so
        // sharing them across threads is safe without a mutex.
        pool.submit([=, &inputs, &outputs, &any_failure]() {
            for (size_t j = start; j < end; j++) {

                // Guard: ciphertext must carry at least the seal overhead
                if (inputs[j].length < crypto_box_SEALBYTES) {
                    outputs[j].status = SO_ERR_BAD_CIPHER;
                    outputs[j].length = 0;
                    any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
                    continue;   // process the rest of the chunk
                }

                size_t plaintext_len = inputs[j].length - crypto_box_SEALBYTES;

                // outputs[j].plaintext must be pre-allocated by the caller
                // (Go layer) to at least plaintext_len bytes.
                int res = crypto_box_seal_open(
                    outputs[j].plaintext,       // destination buffer
                    inputs[j].ciphertext,       // source sealed box
                    inputs[j].length,           // full ciphertext length
                    pub_key,
                    priv_key
                );

                if (res != 0) {
                    outputs[j].status = SO_ERR_BAD_CIPHER;
                    outputs[j].length = 0;
                    any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
                } else {
                    outputs[j].length         = plaintext_len;
                    outputs[j].original_index = static_cast<uint32_t>(j);
                    outputs[j].status         = SO_SUCCESS;
                }
            }
        });
    }

    // Block until every submitted chunk task has finished executing
    pool.wait_all();

    return any_failure.load();
}