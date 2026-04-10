#ifndef SECURE_ORDER_CGO_WRAPPER_H
#define SECURE_ORDER_CGO_WRAPPER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define SO_SUCCESS 0
#define SO_ERR_BAD_CIPHER -1
#define SO_ERR_KEY_MISMATCH -2
#define SO_ERR_BUFFER_SMALL -3
#define SO_ERR_INIT_FAILED -4

#define SO_PUBLIC_KEY_BYTES 32
#define SO_SECRET_KEY_BYTES 32
#define SO_SEAL_BYTES 48

typedef struct {
    uint8_t *ciphertext;
    size_t length;
} EncryptedTx;

typedef struct {
    uint8_t *plaintext;
    size_t length;
    uint32_t original_index;
    int status;
} DecryptedTx;

int init_privacy_layer(void);
int generate_sequencer_keys(uint8_t *public_key, uint8_t *private_key);
int save_key_to_file(const char *filepath, const uint8_t *key, size_t len);
int load_key_from_file(const char *filepath, uint8_t *buffer, size_t len);
int seal_transaction(
    const uint8_t *plaintext,
    size_t len,
    const uint8_t *seq_pub_key,
    uint8_t *ciphertext
);
int decrypt_single_tx(
    const EncryptedTx *input,
    const uint8_t *pub_key,
    const uint8_t *priv_key,
    DecryptedTx *output
);
int decrypt_batch_tx(
    const EncryptedTx *inputs,
    size_t batch_size,
    const uint8_t *pub_key,
    const uint8_t *priv_key,
    DecryptedTx *outputs
);

#ifdef __cplusplus
}
#endif

#endif
