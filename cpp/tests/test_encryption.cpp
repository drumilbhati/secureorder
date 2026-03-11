/**
 * test_encryption.cpp
 *
 * Unit and integration tests for the Secure-Order NaCl Sealed Box layer.
 *
 * Tests are grouped into sections:
 *   1.  Library Initialisation
 *   2.  Key Generation
 *   3.  Seal / Open Round-Trip (single transaction)
 *   4.  Tamper Detection (MAC integrity)
 *   5.  Raw byte seal / open
 *   6.  Batch Decryption (parallel path)
 *   7.  Key persistence (save / load)
 *   8.  Encrypted Mempool
 *   9.  Edge Cases
 *
 * Build (with tests enabled):
 *   mkdir build && cd build
 *   cmake .. -DBUILD_TESTS=ON
 *   make
 *   ctest --output-on-failure
 *
 * Or run directly: ./test_encryption
 *
 * Exit code: 0 = all tests passed, non-zero = at least one failure.
 */

#include "../include/privacy/encryption.h"
#include "../include/privacy/encrypted_mempool.hpp"
#include "../include/privacy/batch_decryptor.h"

#include <sodium.h>

#include <cassert>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>
#include <cstdlib>   // mkstemp / _tempnam on Windows
#include <ctime>

// ─── Minimal test harness ────────────────────────────────────────────────────

static int total_tests  = 0;
static int passed_tests = 0;
static int failed_tests = 0;

// PASS / FAIL macros — never abort on failure so every test runs
#define TEST(name)        do { total_tests++; printf("[TEST] %-60s", name); } while(0)
#define PASS()            do { passed_tests++; printf("  PASS\n"); } while(0)
#define FAIL(msg)         do { failed_tests++; printf("  FAIL  (%s)\n", msg); } while(0)
#define CHECK(cond, msg)  do { if (!(cond)) { FAIL(msg); return; } } while(0)
#define CHECK_EQ(a,b,msg) do { if ((a) != (b)) { FAIL(msg); return; } } while(0)

// ─── Helpers ─────────────────────────────────────────────────────────────────

/** Build a realistic-looking transaction payload as raw bytes. */
static std::vector<uint8_t> make_payload(const char* trade_type,
                                          const char* pair,
                                          double       amount,
                                          double       price)
{
    char buf[256];
    int n = snprintf(buf, sizeof(buf),
                     "TRADE|%s|%s|%.6f|%.6f|%llu",
                     trade_type, pair, amount, price,
                     (unsigned long long)time(nullptr));
    // snprintf won't null-terminate if output is truncated, but we use 'n' bytes
    return std::vector<uint8_t>(reinterpret_cast<uint8_t*>(buf),
                                reinterpret_cast<uint8_t*>(buf) + n);
}

/** Helper: generate a fresh keypair, assert both calls succeed. */
static void make_keypair(uint8_t pub[crypto_box_PUBLICKEYBYTES],
                         uint8_t sec[crypto_box_SECRETKEYBYTES])
{
    int r1 = init_privacy_layer();
    assert(r1 == SO_SUCCESS || r1 == SO_SUCCESS); // idempotent
    int r2 = generate_sequencer_keys(pub, sec);
    assert(r2 == SO_SUCCESS);
}

// ─── Section 1: Library Initialisation ───────────────────────────────────────

void test_init_once() {
    TEST("init_privacy_layer: first call returns SO_SUCCESS");
    int ret = init_privacy_layer();
    CHECK(ret == SO_SUCCESS, "expected SO_SUCCESS");
    PASS();
}

void test_init_idempotent() {
    TEST("init_privacy_layer: repeated calls are idempotent (no error)");
    int r1 = init_privacy_layer();
    int r2 = init_privacy_layer();
    int r3 = init_privacy_layer();
    CHECK(r1 == SO_SUCCESS, "first call failed");
    CHECK(r2 == SO_SUCCESS, "second call failed");
    CHECK(r3 == SO_SUCCESS, "third call failed");
    PASS();
}

// ─── Section 2: Key Generation ───────────────────────────────────────────────

void test_keygen_produces_nonzero_keys() {
    TEST("generate_sequencer_keys: produced keys are not all-zeros");
    init_privacy_layer();

    uint8_t pub[crypto_box_PUBLICKEYBYTES] = {0};
    uint8_t sec[crypto_box_SECRETKEYBYTES] = {0};
    int ret = generate_sequencer_keys(pub, sec);

    CHECK_EQ(ret, SO_SUCCESS, "keygen returned error");

    // Both buffers must contain at least one non-zero byte
    bool pub_nonzero = false, sec_nonzero = false;
    for (size_t i = 0; i < crypto_box_PUBLICKEYBYTES; i++)
        if (pub[i]) { pub_nonzero = true; break; }
    for (size_t i = 0; i < crypto_box_SECRETKEYBYTES; i++)
        if (sec[i]) { sec_nonzero = true; break; }

    CHECK(pub_nonzero, "public key is all zeros");
    CHECK(sec_nonzero, "secret key is all zeros");
    PASS();
}

void test_keygen_different_each_call() {
    TEST("generate_sequencer_keys: two calls produce different keypairs");
    init_privacy_layer();

    uint8_t pub1[crypto_box_PUBLICKEYBYTES], sec1[crypto_box_SECRETKEYBYTES];
    uint8_t pub2[crypto_box_PUBLICKEYBYTES], sec2[crypto_box_SECRETKEYBYTES];

    generate_sequencer_keys(pub1, sec1);
    generate_sequencer_keys(pub2, sec2);

    bool pub_differs = memcmp(pub1, pub2, crypto_box_PUBLICKEYBYTES) != 0;
    bool sec_differs = memcmp(sec1, sec2, crypto_box_SECRETKEYBYTES) != 0;

    CHECK(pub_differs, "public keys are identical (RNG collision or not random)");
    CHECK(sec_differs, "secret keys are identical (RNG collision or not random)");
    PASS();
}

void test_keygen_correct_output_size() {
    TEST("generate_sequencer_keys: output buffers have expected sizes (32 bytes each)");
    // The sizes are compile-time constants from libsodium; this is a sanity check
    CHECK_EQ((int)crypto_box_PUBLICKEYBYTES, 32, "unexpected public key size");
    CHECK_EQ((int)crypto_box_SECRETKEYBYTES, 32, "unexpected secret key size");
    CHECK_EQ((int)crypto_box_SEALBYTES,      48, "unexpected seal overhead");
    PASS();
}

// ─── Section 3: Seal / Open Round-Trip ───────────────────────────────────────

void test_seal_open_roundtrip_basic() {
    TEST("seal → open: basic round-trip recovers original plaintext");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    auto payload    = make_payload("BUY", "ETH/USDC", 1.5, 3200.0);
    size_t pt_len   = payload.size();
    size_t ct_len   = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ciphertext(ct_len);
    int r1 = seal_transaction(payload.data(), pt_len, pub, ciphertext.data());
    CHECK_EQ(r1, SO_SUCCESS, "seal_transaction failed");

    // Wrap in EncryptedTx struct
    EncryptedTx enc;
    enc.ciphertext = ciphertext.data();
    enc.length     = ct_len;

    std::vector<uint8_t> recovered(pt_len);
    DecryptedTx dec;
    dec.plaintext = recovered.data();
    dec.length    = 0;
    dec.status    = -99;

    int r2 = decrypt_single_tx(&enc, pub, sec, &dec);
    CHECK_EQ(r2,          SO_SUCCESS, "decrypt_single_tx returned error");
    CHECK_EQ(dec.status,  SO_SUCCESS, "dec.status not SO_SUCCESS");
    CHECK_EQ(dec.length,  pt_len,     "decrypted length mismatch");
    CHECK(memcmp(recovered.data(), payload.data(), pt_len) == 0,
          "decrypted content does not match original");
    PASS();
}

void test_seal_open_sell_transaction() {
    TEST("seal → open: SELL transaction recovers correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    auto payload  = make_payload("SELL", "BTC/USDC", 0.25, 62000.0);
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ciphertext(ct_len);
    seal_transaction(payload.data(), pt_len, pub, ciphertext.data());

    EncryptedTx enc { ciphertext.data(), ct_len };
    std::vector<uint8_t> recovered(pt_len);
    DecryptedTx dec { recovered.data(), 0, 0, -99 };

    int r = decrypt_single_tx(&enc, pub, sec, &dec);
    CHECK_EQ(r, SO_SUCCESS, "decrypt failed");
    CHECK(memcmp(recovered.data(), payload.data(), pt_len) == 0,
          "SELL transaction content mismatch");
    PASS();
}

void test_seal_open_large_payload() {
    TEST("seal → open: large payload (4096 bytes) round-trips correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    // Simulate a large transaction batch descriptor
    std::vector<uint8_t> payload(4096);
    randombytes_buf(payload.data(), payload.size()); // fill with random bytes
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ciphertext(ct_len);
    seal_transaction(payload.data(), pt_len, pub, ciphertext.data());

    EncryptedTx enc { ciphertext.data(), ct_len };
    std::vector<uint8_t> recovered(pt_len);
    DecryptedTx dec { recovered.data(), 0, 0, -99 };
    decrypt_single_tx(&enc, pub, sec, &dec);

    CHECK_EQ(dec.length, pt_len, "large payload length mismatch");
    CHECK(memcmp(recovered.data(), payload.data(), pt_len) == 0,
          "large payload content mismatch");
    PASS();
}

void test_seal_produces_different_ciphertexts() {
    TEST("seal: same plaintext sealed twice produces different ciphertexts (ephemeral key)");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    auto payload  = make_payload("BUY", "ETH/USDC", 1.0, 3000.0);
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ct1(ct_len), ct2(ct_len);
    seal_transaction(payload.data(), pt_len, pub, ct1.data());
    seal_transaction(payload.data(), pt_len, pub, ct2.data());

    // The two ciphertexts must differ (different ephemeral keys each time)
    CHECK(memcmp(ct1.data(), ct2.data(), ct_len) != 0,
          "same plaintext produced identical ciphertext — ephemeral key not random");
    PASS();
}

// ─── Section 4: Tamper Detection ─────────────────────────────────────────────

void test_tamper_single_byte_detected() {
    TEST("tamper: flipping one byte in ciphertext causes decryption failure");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    auto payload  = make_payload("BUY", "ETH/USDC", 2.0, 3100.0);
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ciphertext(ct_len);
    seal_transaction(payload.data(), pt_len, pub, ciphertext.data());

    // Flip a byte in the middle of the ciphertext
    ciphertext[ct_len / 2] ^= 0xFF;

    EncryptedTx enc { ciphertext.data(), ct_len };
    std::vector<uint8_t> buf(pt_len, 0);
    DecryptedTx dec { buf.data(), 0, 0, SO_SUCCESS };

    int r = decrypt_single_tx(&enc, pub, sec, &dec);
    CHECK(r != SO_SUCCESS, "tampered ciphertext was accepted (CRITICAL)");
    CHECK(dec.status != SO_SUCCESS, "dec.status should indicate failure");
    PASS();
}

void test_tamper_wrong_key_rejected() {
    TEST("tamper: decrypting with wrong secret key returns error");
    uint8_t pub1[crypto_box_PUBLICKEYBYTES], sec1[crypto_box_SECRETKEYBYTES];
    uint8_t pub2[crypto_box_PUBLICKEYBYTES], sec2[crypto_box_SECRETKEYBYTES];
    make_keypair(pub1, sec1);
    make_keypair(pub2, sec2); // different keypair

    auto payload  = make_payload("SELL", "BTC/USDC", 0.1, 61000.0);
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ciphertext(ct_len);
    seal_transaction(payload.data(), pt_len, pub1, ciphertext.data()); // sealed for keypair 1

    EncryptedTx enc { ciphertext.data(), ct_len };
    std::vector<uint8_t> buf(pt_len, 0);
    DecryptedTx dec { buf.data(), 0, 0, SO_SUCCESS };

    // Try to open with keypair 2 — must fail
    int r = decrypt_single_tx(&enc, pub2, sec2, &dec);
    CHECK(r != SO_SUCCESS, "wrong-key decryption was accepted (CRITICAL)");
    PASS();
}

void test_tamper_truncated_ciphertext_rejected() {
    TEST("tamper: ciphertext shorter than seal overhead is rejected");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    // Provide a ciphertext that is too short to be a valid sealed box
    std::vector<uint8_t> bad_ct(crypto_box_SEALBYTES - 1, 0xAB);
    EncryptedTx enc { bad_ct.data(), bad_ct.size() };

    std::vector<uint8_t> buf(64, 0);
    DecryptedTx dec { buf.data(), 0, 0, SO_SUCCESS };

    int r = decrypt_single_tx(&enc, pub, sec, &dec);
    CHECK(r != SO_SUCCESS, "truncated ciphertext was accepted (CRITICAL)");
    PASS();
}

// ─── Section 5: Raw byte seal / open ─────────────────────────────────────────

void test_raw_seal_open_roundtrip() {
    TEST("seal_raw → open via decrypt_single_tx: raw bytes recovered correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    const char *msg  = "raw|BUY|ETH|5.5|3250.00";
    size_t pt_len    = strlen(msg);
    size_t ct_len    = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ciphertext(ct_len);
    int r1 = seal_transaction(
        reinterpret_cast<const uint8_t*>(msg), pt_len, pub, ciphertext.data());
    CHECK_EQ(r1, SO_SUCCESS, "seal_transaction (raw) failed");

    EncryptedTx enc { ciphertext.data(), ct_len };
    std::vector<uint8_t> recovered(pt_len);
    DecryptedTx dec { recovered.data(), 0, 0, -99 };
    decrypt_single_tx(&enc, pub, sec, &dec);

    CHECK_EQ(dec.length, pt_len, "raw bytes: length mismatch after decryption");
    CHECK(memcmp(recovered.data(), msg, pt_len) == 0,
          "raw bytes: content mismatch after decryption");
    PASS();
}

// ─── Section 6: Batch Decryption ─────────────────────────────────────────────

void test_batch_small() {
    TEST("decrypt_batch_tx: batch of 5 transactions all decrypt correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    const int N = 5;
    std::vector<std::vector<uint8_t>> payloads(N);
    std::vector<std::vector<uint8_t>> ciphertexts(N);
    std::vector<std::vector<uint8_t>> recovered(N);

    EncryptedTx  enc_arr[N];
    DecryptedTx  dec_arr[N];

    for (int i = 0; i < N; i++) {
        payloads[i]    = make_payload(i % 2 == 0 ? "BUY" : "SELL",
                                      "ETH/USDC", 1.0 * (i + 1), 3000.0 + i * 10);
        size_t pt_len  = payloads[i].size();
        size_t ct_len  = pt_len + crypto_box_SEALBYTES;
        ciphertexts[i].resize(ct_len);
        seal_transaction(payloads[i].data(), pt_len, pub, ciphertexts[i].data());

        enc_arr[i].ciphertext = ciphertexts[i].data();
        enc_arr[i].length     = ct_len;

        recovered[i].resize(pt_len);
        dec_arr[i].plaintext = recovered[i].data();
        dec_arr[i].length    = 0;
        dec_arr[i].status    = -99;
    }

    int ret = decrypt_batch_tx(enc_arr, N, pub, sec, dec_arr);
    CHECK_EQ(ret, SO_SUCCESS, "decrypt_batch_tx returned error");

    for (int i = 0; i < N; i++) {
        CHECK_EQ(dec_arr[i].status, SO_SUCCESS, "batch item status != SO_SUCCESS");
        CHECK_EQ(dec_arr[i].length, payloads[i].size(), "batch item length mismatch");
        CHECK(memcmp(recovered[i].data(), payloads[i].data(), payloads[i].size()) == 0,
              "batch item content mismatch");
    }
    PASS();
}

void test_batch_large() {
    TEST("decrypt_batch_tx: batch of 100 transactions (stress)");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    const int N = 100;
    std::vector<std::vector<uint8_t>> payloads(N), ciphertexts(N), recovered(N);
    std::vector<EncryptedTx>  enc_arr(N);
    std::vector<DecryptedTx>  dec_arr(N);

    for (int i = 0; i < N; i++) {
        payloads[i]   = make_payload("BUY", "ETH/USDC", (double)i, 3000.0);
        size_t pt_len = payloads[i].size();
        size_t ct_len = pt_len + crypto_box_SEALBYTES;
        ciphertexts[i].resize(ct_len);
        seal_transaction(payloads[i].data(), pt_len, pub, ciphertexts[i].data());

        enc_arr[i] = { ciphertexts[i].data(), ct_len };
        recovered[i].resize(pt_len);
        dec_arr[i] = { recovered[i].data(), 0, (uint32_t)i, -99 };
    }

    int ret = decrypt_batch_tx(enc_arr.data(), N, pub, sec, dec_arr.data());
    CHECK_EQ(ret, SO_SUCCESS, "batch-100 returned error");

    int ok = 0;
    for (int i = 0; i < N; i++)
        if (dec_arr[i].status == SO_SUCCESS) ok++;

    CHECK_EQ(ok, N, "not all 100 items decrypted successfully");
    PASS();
}

void test_batch_partial_tamper() {
    TEST("decrypt_batch_tx: one tampered item fails, rest succeed");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    const int N = 4;
    std::vector<std::vector<uint8_t>> payloads(N), ciphertexts(N), recovered(N);
    EncryptedTx enc_arr[N];
    DecryptedTx dec_arr[N];

    for (int i = 0; i < N; i++) {
        payloads[i]   = make_payload("BUY", "BTC/USDC", (double)(i+1), 60000.0);
        size_t pt_len = payloads[i].size();
        size_t ct_len = pt_len + crypto_box_SEALBYTES;
        ciphertexts[i].resize(ct_len);
        seal_transaction(payloads[i].data(), pt_len, pub, ciphertexts[i].data());
        enc_arr[i] = { ciphertexts[i].data(), ct_len };
        recovered[i].resize(pt_len);
        dec_arr[i] = { recovered[i].data(), 0, (uint32_t)i, -99 };
    }

    // Tamper with index 2
    ciphertexts[2][crypto_box_SEALBYTES] ^= 0x01;

    int ret = decrypt_batch_tx(enc_arr, N, pub, sec, dec_arr);
    // Overall return should indicate at least one failure
    CHECK(ret != SO_SUCCESS, "batch should report failure when one item is tampered");

    // Items 0, 1, 3 must succeed
    CHECK_EQ(dec_arr[0].status, SO_SUCCESS, "item 0 should have succeeded");
    CHECK_EQ(dec_arr[1].status, SO_SUCCESS, "item 1 should have succeeded");
    CHECK(dec_arr[2].status   != SO_SUCCESS, "tampered item 2 should have failed");
    CHECK_EQ(dec_arr[3].status, SO_SUCCESS, "item 3 should have succeeded");
    PASS();
}

void test_batch_class() {
    TEST("BatchDecryptor class: decrypts 10-item batch using thread pool");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    const int N = 10;
    std::vector<std::vector<uint8_t>> payloads(N), ciphertexts(N), recovered(N);
    std::vector<EncryptedTx> enc_arr(N);
    std::vector<DecryptedTx> dec_arr(N);

    for (int i = 0; i < N; i++) {
        payloads[i]   = make_payload("SELL", "ETH/USDC", (double)(i+1)*0.5, 3050.0);
        size_t pt_len = payloads[i].size();
        size_t ct_len = pt_len + crypto_box_SEALBYTES;
        ciphertexts[i].resize(ct_len);
        seal_transaction(payloads[i].data(), pt_len, pub, ciphertexts[i].data());
        enc_arr[i] = { ciphertexts[i].data(), ct_len };
        recovered[i].resize(pt_len);
        dec_arr[i] = { recovered[i].data(), 0, (uint32_t)i, -99 };
    }

    BatchDecryptor bd;
    int ret = bd.decrypt_batch(enc_arr.data(), N, pub, sec, dec_arr.data());
    CHECK_EQ(ret, SO_SUCCESS, "BatchDecryptor returned error");

    for (int i = 0; i < N; i++) {
        CHECK_EQ(dec_arr[i].status, SO_SUCCESS, "BatchDecryptor: item status != SO_SUCCESS");
        CHECK(memcmp(recovered[i].data(), payloads[i].data(), payloads[i].size()) == 0,
              "BatchDecryptor: item content mismatch");
    }
    PASS();
}

// ─── Section 7: Key Persistence (save / load) ────────────────────────────────

void test_save_load_public_key() {
    TEST("save_key_to_file / load_key_from_file: public key persists correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    // Use a temp file path
    const char *path = "tmp_test_pubkey.bin";

    int s = save_key_to_file(path, pub, crypto_box_PUBLICKEYBYTES);
    CHECK_EQ(s, 1, "save_key_to_file failed");

    uint8_t loaded[crypto_box_PUBLICKEYBYTES] = {0};
    int l = load_key_from_file(path, loaded, crypto_box_PUBLICKEYBYTES);
    CHECK_EQ(l, 1, "load_key_from_file failed");

    CHECK(memcmp(pub, loaded, crypto_box_PUBLICKEYBYTES) == 0,
          "loaded public key does not match original");

    remove(path);
    PASS();
}

void test_save_load_secret_key() {
    TEST("save_key_to_file / load_key_from_file: secret key persists correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    const char *path = "tmp_test_seckey.bin";

    save_key_to_file(path, sec, crypto_box_SECRETKEYBYTES);

    uint8_t loaded[crypto_box_SECRETKEYBYTES] = {0};
    load_key_from_file(path, loaded, crypto_box_SECRETKEYBYTES);

    CHECK(memcmp(sec, loaded, crypto_box_SECRETKEYBYTES) == 0,
          "loaded secret key does not match original");

    remove(path);
    PASS();
}

void test_load_missing_file_returns_zero() {
    TEST("load_key_from_file: non-existent file returns 0 (failure)");
    uint8_t buf[32] = {0};
    int r = load_key_from_file("this_file_does_not_exist_xyz.bin", buf, 32);
    CHECK_EQ(r, 0, "expected 0 for missing file, got non-zero");
    PASS();
}

void test_load_wrong_size_returns_zero() {
    TEST("load_key_from_file: requesting more bytes than file contains returns 0");
    // Write a small file
    const char *path = "tmp_small_key.bin";
    uint8_t tiny[8] = {1,2,3,4,5,6,7,8};
    save_key_to_file(path, tiny, 8);

    // Try to load 32 bytes from an 8-byte file
    uint8_t buf[32] = {0};
    int r = load_key_from_file(path, buf, 32);
    CHECK_EQ(r, 0, "expected 0 for short file, got non-zero");

    remove(path);
    PASS();
}

void test_persisted_keys_enable_decryption() {
    TEST("full cycle: generate → save keys → load keys → seal → open");
    uint8_t pub_orig[crypto_box_PUBLICKEYBYTES], sec_orig[crypto_box_SECRETKEYBYTES];
    make_keypair(pub_orig, sec_orig);

    const char *pub_path = "tmp_cycle_pub.bin";
    const char *sec_path = "tmp_cycle_sec.bin";
    save_key_to_file(pub_path, pub_orig, crypto_box_PUBLICKEYBYTES);
    save_key_to_file(sec_path, sec_orig, crypto_box_SECRETKEYBYTES);

    // Simulate reboot: load keys from disk
    uint8_t pub_loaded[crypto_box_PUBLICKEYBYTES], sec_loaded[crypto_box_SECRETKEYBYTES];
    load_key_from_file(pub_path, pub_loaded, crypto_box_PUBLICKEYBYTES);
    load_key_from_file(sec_path, sec_loaded, crypto_box_SECRETKEYBYTES);

    // Seal with loaded public key
    auto payload  = make_payload("BUY", "SOL/USDC", 10.0, 150.0);
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;
    std::vector<uint8_t> ct(ct_len);
    seal_transaction(payload.data(), pt_len, pub_loaded, ct.data());

    // Open with loaded secret key
    EncryptedTx enc { ct.data(), ct_len };
    std::vector<uint8_t> recovered(pt_len);
    DecryptedTx dec { recovered.data(), 0, 0, -99 };
    decrypt_single_tx(&enc, pub_loaded, sec_loaded, &dec);

    CHECK_EQ(dec.status, SO_SUCCESS, "decryption with loaded keys failed");
    CHECK(memcmp(recovered.data(), payload.data(), pt_len) == 0,
          "content mismatch after key persistence cycle");

    remove(pub_path);
    remove(sec_path);
    PASS();
}

// ─── Section 8: Encrypted Mempool ────────────────────────────────────────────

void test_mempool_add_get() {
    TEST("encrypted_mempool: add then get returns the same bytes");
    encrypted_mempool mp;
    std::vector<uint8_t> data = {0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02};
    mp.add_transaction("tx-0001", data);

    auto retrieved = mp.get_transaction("tx-0001");
    CHECK_EQ(retrieved.size(), data.size(), "retrieved size mismatch");
    CHECK(memcmp(retrieved.data(), data.data(), data.size()) == 0,
          "retrieved bytes do not match stored bytes");
    PASS();
}

void test_mempool_remove() {
    TEST("encrypted_mempool: remove makes key unavailable (throws std::out_of_range)");
    encrypted_mempool mp;
    std::vector<uint8_t> data = {0x01, 0x02, 0x03};
    mp.add_transaction("tx-remove", data);
    mp.remove_transaction("tx-remove");

    bool threw = false;
    try {
        mp.get_transaction("tx-remove");
    } catch (const std::out_of_range&) {
        threw = true;
    }
    CHECK(threw, "get_transaction should throw after remove");
    PASS();
}

void test_mempool_overwrite() {
    TEST("encrypted_mempool: adding same key twice overwrites old value");
    encrypted_mempool mp;
    std::vector<uint8_t> v1 = {0xAA, 0xBB};
    std::vector<uint8_t> v2 = {0xCC, 0xDD, 0xEE};
    mp.add_transaction("tx-dup", v1);
    mp.add_transaction("tx-dup", v2);  // overwrite

    auto got = mp.get_transaction("tx-dup");
    CHECK_EQ(got.size(), v2.size(), "overwritten value has wrong size");
    CHECK(memcmp(got.data(), v2.data(), v2.size()) == 0,
          "overwritten value has wrong content");
    PASS();
}

void test_mempool_stores_sealed_transactions() {
    TEST("encrypted_mempool: stores real sealed transactions correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    auto payload  = make_payload("BUY", "ETH/USDC", 3.0, 3300.0);
    size_t pt_len = payload.size();
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> sealed(ct_len);
    seal_transaction(payload.data(), pt_len, pub, sealed.data());

    encrypted_mempool mp;
    mp.add_transaction("tx-sealed-001", sealed);

    auto retrieved = mp.get_transaction("tx-sealed-001");

    // Decrypt what was retrieved
    EncryptedTx enc { retrieved.data(), retrieved.size() };
    std::vector<uint8_t> recovered(pt_len);
    DecryptedTx dec { recovered.data(), 0, 0, -99 };
    int r = decrypt_single_tx(&enc, pub, sec, &dec);

    CHECK_EQ(r, SO_SUCCESS, "decrypt after mempool roundtrip failed");
    CHECK(memcmp(recovered.data(), payload.data(), pt_len) == 0,
          "mempool → decrypt content mismatch");
    PASS();
}

// ─── Section 9: Edge Cases ────────────────────────────────────────────────────

void test_empty_batch_returns_success() {
    TEST("decrypt_batch_tx: empty batch (0 items) returns SO_SUCCESS");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    int r = decrypt_batch_tx(nullptr, 0, pub, sec, nullptr);
    CHECK_EQ(r, SO_SUCCESS, "empty batch should return SO_SUCCESS");
    PASS();
}

void test_single_byte_payload() {
    TEST("seal → open: single-byte payload round-trips correctly");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    uint8_t plain = 0x42;
    size_t pt_len = 1;
    size_t ct_len = pt_len + crypto_box_SEALBYTES;

    std::vector<uint8_t> ct(ct_len);
    seal_transaction(&plain, pt_len, pub, ct.data());

    EncryptedTx enc { ct.data(), ct_len };
    uint8_t result = 0;
    DecryptedTx dec { &result, 0, 0, -99 };
    decrypt_single_tx(&enc, pub, sec, &dec);

    CHECK_EQ(dec.status, SO_SUCCESS, "single-byte failed to decrypt");
    CHECK_EQ(result, plain, "single-byte content wrong");
    PASS();
}

void test_ciphertext_hides_plaintext() {
    TEST("privacy: ciphertext shares no obvious prefix/suffix with plaintext");
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    make_keypair(pub, sec);

    // Use a repeated-byte plaintext that would be trivially visible if unencrypted
    std::vector<uint8_t> payload(32, 0xAA);
    size_t ct_len = payload.size() + crypto_box_SEALBYTES;

    std::vector<uint8_t> ct(ct_len);
    seal_transaction(payload.data(), payload.size(), pub, ct.data());

    // Count how many bytes in the ciphertext equal 0xAA
    int matches = 0;
    for (auto b : ct) if (b == 0xAA) matches++;

    // In a truly random-looking ciphertext ~1/256 bytes ≈ 0.4% should match.
    // If more than 20% match, the encryption is leaking the plaintext pattern.
    float pct = (float)matches / ct_len * 100.0f;
    CHECK(pct < 20.0f, "too many plaintext bytes visible in ciphertext (not encrypted)");
    PASS();
}

// ─── Main ─────────────────────────────────────────────────────────────────────

int main() {
    printf("\n");
    printf("=================================================================\n");
    printf("  Secure-Order :: NaCl Sealed Box Privacy Layer — Unit Tests\n");
    printf("=================================================================\n\n");

    // Section 1: Initialisation
    printf("--- 1. Library Initialisation ---\n");
    test_init_once();
    test_init_idempotent();

    // Section 2: Key Generation
    printf("\n--- 2. Key Generation ---\n");
    test_keygen_produces_nonzero_keys();
    test_keygen_different_each_call();
    test_keygen_correct_output_size();

    // Section 3: Seal / Open Round-Trip
    printf("\n--- 3. Seal / Open Round-Trip ---\n");
    test_seal_open_roundtrip_basic();
    test_seal_open_sell_transaction();
    test_seal_open_large_payload();
    test_seal_produces_different_ciphertexts();

    // Section 4: Tamper Detection
    printf("\n--- 4. Tamper Detection (MAC Integrity) ---\n");
    test_tamper_single_byte_detected();
    test_tamper_wrong_key_rejected();
    test_tamper_truncated_ciphertext_rejected();

    // Section 5: Raw
    printf("\n--- 5. Raw Byte Seal / Open ---\n");
    test_raw_seal_open_roundtrip();

    // Section 6: Batch
    printf("\n--- 6. Batch Decryption (Parallel) ---\n");
    test_batch_small();
    test_batch_large();
    test_batch_partial_tamper();
    test_batch_class();

    // Section 7: Key Persistence
    printf("\n--- 7. Key Persistence ---\n");
    test_save_load_public_key();
    test_save_load_secret_key();
    test_load_missing_file_returns_zero();
    test_load_wrong_size_returns_zero();
    test_persisted_keys_enable_decryption();

    // Section 8: Encrypted Mempool
    printf("\n--- 8. Encrypted Mempool ---\n");
    test_mempool_add_get();
    test_mempool_remove();
    test_mempool_overwrite();
    test_mempool_stores_sealed_transactions();

    // Section 9: Edge Cases
    printf("\n--- 9. Edge Cases ---\n");
    test_empty_batch_returns_success();
    test_single_byte_payload();
    test_ciphertext_hides_plaintext();

    // Summary
    printf("\n=================================================================\n");
    printf("  Results: %d / %d tests passed", passed_tests, total_tests);
    if (failed_tests > 0)
        printf("  (%d FAILED)", failed_tests);
    printf("\n");
    printf("=================================================================\n\n");

    return (failed_tests > 0) ? 1 : 0;
}
