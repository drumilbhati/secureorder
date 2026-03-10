#ifndef SECURE_ORDER_H
#define SECURE_ORDER_H

#include <cstddef>
#include <cstdint>
#include <stddef.h>
#include <stdint.h>

// This ensures C++ compilers dont mangle names,
// allowing Go compiler (CGO)
#ifdef __cplusplus
extern "C" {
#endif

// Error Codes

#define SO_SUCCESS           0
#define SO_ERR_BAD_CIPHER   -1
#define SO_ERR_KEY_MISMATCH -2
#define SO_ERR_BUFFER_SMALL -3
#define SO_ERR_INIT_FAILED  -4   // sodium_init() failed

// Data Structures

// Represents an encrypted transaction (NaCl sealed box)
// ciphertext is (plaintext_len + crypto_box_SEALBYTES) bytes long
typedef struct {
    uint8_t *ciphertext;
    size_t   length;      // total ciphertext length including seal overhead
} EncryptedTx;

// Represents a decrypted transaction ready for execution
typedef struct {
    uint8_t  *plaintext;
    size_t    length;          // plaintext length = ciphertext_length - crypto_box_SEALBYTES
    uint32_t  original_index;  // position in the original ordered batch
    int       status;          // SO_SUCCESS or per-item error code
} DecryptedTx;

// Lifecycle

/*
 * Must be called once at sequencer startup before any crypto operations.
 * Internally calls sodium_init().
 * Returns SO_SUCCESS (0) or SO_ERR_INIT_FAILED (-4).
 * NOTE: sodium_init() returns 1 if already initialised — that is NOT an error.
 */
int init_privacy_layer(void);

// Key Management

/*
 * Generates a Curve25519 keypair for the sequencer.
 * public_key  must point to a buffer of at least crypto_box_PUBLICKEYBYTES (32) bytes.
 * private_key must point to a buffer of at least crypto_box_SECRETKEYBYTES (32) bytes.
 * Returns SO_SUCCESS on success.
 */
int generate_sequencer_keys(uint8_t *public_key, uint8_t *private_key);

/*
 * Saves a key to a binary file on disk.
 * filepath: null-terminated path string (CGO-compatible, no std::string).
 * Returns 1 on success, 0 on failure.
 */
int save_key_to_file(const char *filepath, const uint8_t *key, size_t len);

/*
 * Loads a key from a binary file into buffer.
 * filepath: null-terminated path string.
 * Returns 1 on success, 0 on failure.
 */
int load_key_from_file(const char *filepath, uint8_t *buffer, size_t len);

// Single Transaction (Client Side)

/*
 * Encrypts a transaction using the sequencer's public key.
 * The sender remains anonymous (NaCl sealed box — no sender keypair needed).
 * ciphertext must be pre-allocated to at least (len + crypto_box_SEALBYTES) bytes.
 * Returns SO_SUCCESS or SO_ERR_BAD_CIPHER.
 */
int seal_transaction(
    const uint8_t *plaintext,
    size_t         len,
    const uint8_t *seq_pub_key,
    uint8_t       *ciphertext
);

// Single Transaction (Sequencer Side — Baseline)

/*
 * Decrypts a single transaction using the sequencer's keypair.
 * Baseline implementation — used for correctness reference and benchmarking.
 * input->length is the full ciphertext length (including seal overhead).
 * output->plaintext must be pre-allocated to at least
 *   (input->length - crypto_box_SEALBYTES) bytes.
 * Sets output->length and output->status on return.
 * Returns SO_SUCCESS or SO_ERR_KEY_MISMATCH.
 */
int decrypt_single_tx(
    const EncryptedTx *input,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *output
);

// Batch Decryption (Sequencer Side — Optimised)

/*
 * Decrypts a batch of transactions in parallel using the thread pool.
 * inputs      : array of EncryptedTx, length = batch_size.
 * batch_size  : number of transactions in the batch.
 * pub_key     : sequencer public key  (crypto_box_PUBLICKEYBYTES bytes).
 * priv_key    : sequencer private key (crypto_box_SECRETKEYBYTES bytes).
 * outputs     : caller-pre-allocated array of DecryptedTx, length = batch_size.
 *               Each outputs[i].plaintext must also be pre-allocated by the caller.
 *               Each outputs[i].status is set individually — a single bad
 *               ciphertext does NOT abort the rest of the batch.
 * Returns SO_SUCCESS if ALL items decrypted successfully,
 *         SO_ERR_BAD_CIPHER if one or more items failed (check per-item status).
 */
int decrypt_batch_tx(
    const EncryptedTx *inputs,
    size_t             batch_size,
    const uint8_t     *pub_key,
    const uint8_t     *priv_key,
    DecryptedTx       *outputs    // NOT const — we write into this
);

#ifdef __cplusplus
}
#endif

#endif // SECURE_ORDER_H