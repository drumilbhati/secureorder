#ifndef BATCH_DECRYPTOR_H
#define BATCH_DECRYPTOR_H

#include "thread_pool.h"
#include "encryption.h"
#include <cstdint>

class BatchDecryptor {
public:
    // Initialise with a pool size equal to CPU cores
    BatchDecryptor();

    /*
     * Orchestrates parallel decryption
     * @param inputs Pointer to the encrypted transactions array from Go
     * @param batch_size Total number of transactions
     * @param pub_key Sequencer public key
     * @param priv_key Sequencer private key
     * @param outputs Pre-allocated output buffer (allocated by Go)
     */
    int decrypt_batch(const EncryptedTx* inputs, size_t batch_size, const uint8_t *pub_key, const uint8_t *priv_key, DecryptedTx* outputs);

private:
    ThreadPool pool;
};

#endif
