/*
 * batch_decryptor.cpp — persistent-thread-pool batch decryption.
 *
 * This class is the production-optimised alternative to decrypt_batch_tx in
 * encryption.cpp. The key difference is that BatchDecryptor keeps its thread
 * pool alive between batches. Instead of spawning and joining N threads per
 * batch, worker threads are created once at construction and reused for every
 * subsequent decrypt_batch() call. This eliminates the per-batch thread-creation
 * overhead, which is significant when batch windows are short (sub-second).
 *
 * Use decrypt_batch_tx (encryption.cpp) when you need a stateless call.
 * Use BatchDecryptor when the same instance will process many batches over time.
 */
#include "../../include/privacy/batch_decryptor.h"
#include "../../include/privacy/encryption.h"
#include "../../include/privacy/thread_pool.h"
#include <algorithm>
#include <atomic>
#include <cstdint>
#include <thread>
#include <sodium.h>

/*
 * Constructor: creates the persistent worker pool sized to hardware_concurrency.
 * The ThreadPool constructor spawns all worker threads immediately so they are
 * ready to accept tasks on the first decrypt_batch() call.
 */
BatchDecryptor::BatchDecryptor() : pool(std::thread::hardware_concurrency()) {}

/*
 * decrypt_batch — decrypt a batch of transactions using the persistent thread pool.
 *
 * Algorithm overview:
 *   1. Determine the number of worker threads (min of hardware_concurrency and
 *      batch_size — no point submitting empty chunks).
 *   2. Divide the batch into contiguous chunks using ceiling division so the
 *      last chunk picks up any remainder items.
 *   3. Submit one task per chunk to the thread pool. Each task iterates its
 *      chunk and calls crypto_box_seal_open for every item independently.
 *   4. Call pool.wait_all() to block until every submitted task has finished.
 *   5. Return SO_SUCCESS if all items succeeded, SO_ERR_BAD_CIPHER otherwise.
 *
 * Error handling:
 *   - A single bad ciphertext sets that item's status to SO_ERR_BAD_CIPHER and
 *     sets any_failure atomically, but does NOT abort the rest of the chunk.
 *   - any_failure is read once after wait_all() for the aggregate return value.
 *   - Per-item outcomes are available in outputs[i].status for fine-grained
 *     error reporting in the Go fallback loop.
 *
 * Memory ownership:
 *   - inputs  and outputs are caller-owned arrays; we only read/write them.
 *   - outputs[j].plaintext must be pre-allocated by the caller (Go layer) to
 *     at least (inputs[j].length - crypto_box_SEALBYTES) bytes.
 *   - outputs[j].original_index is set on success so downstream code can
 *     reassemble results without relying on execution order.
 *
 * Thread safety:
 *   - pub_key and priv_key are read-only shared data — no mutex needed.
 *   - Each thread writes only to its own disjoint chunk of outputs[], so
 *     there are no write-write races on the output array.
 *   - any_failure is written with relaxed ordering because we only need the
 *     final value after wait_all() provides the synchronisation barrier.
 */
int BatchDecryptor::decrypt_batch(
    const EncryptedTx *inputs,
    size_t             batch_size,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *outputs)
{
    if (batch_size == 0) return SO_SUCCESS;

    // Cap at batch_size to avoid submitting empty chunks when the batch is
    // smaller than the number of available CPU cores.
    size_t num_threads = std::thread::hardware_concurrency();
    if (num_threads == 0) num_threads = 2;
    num_threads = std::min(num_threads, batch_size);

    // any_failure: SO_SUCCESS (0) until a worker sets it to SO_ERR_BAD_CIPHER.
    // Never reset back to SO_SUCCESS once set.
    std::atomic<int> any_failure{SO_SUCCESS};

    // Ceiling division: ensures last chunk covers any remainder.
    // e.g. 10 items / 3 threads → chunks [0,4), [4,8), [8,10)
    size_t chunk_size = (batch_size + num_threads - 1) / num_threads;

    for (size_t t = 0; t < num_threads; t++) {
        size_t start = t * chunk_size;
        size_t end   = std::min(start + chunk_size, batch_size);

        // Safety guard: should never trigger given the num_threads cap above,
        // but prevents submitting zero-length chunks if arithmetic is off.
        if (start >= batch_size) break;

        // Capture start/end by value (different per iteration).
        // Capture arrays and any_failure by reference — they outlive wait_all().
        // pub_key and priv_key captured by pointer value (read-only, safe).
        pool.submit([=, &inputs, &outputs, &any_failure]() {
            for (size_t j = start; j < end; j++) {

                // Structural guard: ciphertext must carry at least the seal overhead.
                // Passing a too-short buffer to libsodium would cause an out-of-bounds read.
                if (inputs[j].length < crypto_box_SEALBYTES) {
                    outputs[j].status = SO_ERR_BAD_CIPHER;
                    outputs[j].length = 0;
                    any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
                    continue; // keep processing the rest of this chunk
                }

                size_t plaintext_len = inputs[j].length - crypto_box_SEALBYTES;

                /*
                 * crypto_box_seal_open:
                 *   1. Reads the 32-byte ephemeral pubkey from inputs[j].ciphertext.
                 *   2. Derives shared secret via Curve25519 DH.
                 *   3. Verifies the 16-byte Poly1305 MAC.
                 *   4. Decrypts with XSalsa20 into outputs[j].plaintext.
                 * Returns 0 on success, -1 on authentication failure.
                 */
                int res = crypto_box_seal_open(
                    outputs[j].plaintext,   // pre-allocated by Go caller
                    inputs[j].ciphertext,   // full sealed box
                    inputs[j].length,       // ciphertext + seal overhead
                    pub_key,
                    priv_key
                );

                if (res != 0) {
                    outputs[j].status = SO_ERR_BAD_CIPHER;
                    outputs[j].length = 0;
                    any_failure.store(SO_ERR_BAD_CIPHER, std::memory_order_relaxed);
                } else {
                    outputs[j].length         = plaintext_len;
                    outputs[j].original_index = static_cast<uint32_t>(j); // for reassembly
                    outputs[j].status         = SO_SUCCESS;
                }
            }
        });
    }

    // Block until every chunk task has completed. wait_all() provides the
    // happens-before guarantee that makes all relaxed atomic writes to
    // any_failure visible in the load below.
    pool.wait_all();

    return any_failure.load();
}
