/**
 * bench_decryption.cpp
 *
 * Performance benchmarks for the Secure-Order NaCl Sealed Box layer.
 *
 * Measures:
 *   1. Key generation throughput
 *   2. Single seal (encrypt) throughput
 *   3. Single decrypt (decrypt_single_tx) throughput
 *   4. Batch decrypt (decrypt_batch_tx) throughput at various batch sizes
 *   5. BatchDecryptor class (persistent thread pool) throughput
 *   6. Seal + decrypt end-to-end latency (single transaction simulation)
 *
 * Build:
 *   cd cpp && mkdir -p build && cd build
 *   cmake .. -DBUILD_TESTS=ON
 *   make bench_decryption
 *   ./bench_decryption
 *
 * Interpretation guide:
 *   - "tx/sec"  = transactions per second
 *   - "µs/tx"   = microseconds per transaction
 *   - All measurements use monotonic clock (CLOCK_MONOTONIC / chrono::steady_clock)
 *     to avoid wall-clock drift from NTP or system sleep.
 */

#include "../include/privacy/encryption.h"
#include "../include/privacy/batch_decryptor.h"

#include <sodium.h>
#include <chrono>
#include <cstdio>
#include <cstring>
#include <ctime>
#include <string>
#include <vector>

// ─── Utilities ────────────────────────────────────────────────────────────────

using Clock = std::chrono::steady_clock;
using Ns    = std::chrono::nanoseconds;

static inline double elapsed_ms(Clock::time_point start, Clock::time_point end) {
    return std::chrono::duration<double, std::milli>(end - start).count();
}

static inline double throughput(size_t count, double duration_ms) {
    return (count / duration_ms) * 1000.0; // tx/sec
}

static inline double latency_us(double duration_ms, size_t count) {
    return (duration_ms / count) * 1000.0; // µs per tx
}

static void print_separator() {
    printf("─────────────────────────────────────────────────────────────\n");
}

static void print_result(const char* label, size_t count,
                         double dur_ms, const char* notes = "") {
    double tps = throughput(count, dur_ms);
    double lat = latency_us(dur_ms, count);
    printf("  %-35s  %8.0f tx/sec   %6.2f µs/tx  %s\n",
           label, tps, lat, notes);
}

/** Create a realistic sealed transaction. */
static std::vector<uint8_t> make_sealed(const uint8_t pub[crypto_box_PUBLICKEYBYTES],
                                        int index)
{
    char buf[128];
    int n = snprintf(buf, sizeof(buf),
                     "TRADE|BUY|ETH/USDC|%.6f|3000.00|%d",
                     1.0 + index * 0.001, index);
    size_t pt_len = (size_t)n;
    size_t ct_len = pt_len + crypto_box_SEALBYTES;
    std::vector<uint8_t> ct(ct_len);
    seal_transaction(reinterpret_cast<const uint8_t*>(buf), pt_len, pub, ct.data());
    return ct;
}

// ─── Benchmark 1: Key Generation ─────────────────────────────────────────────

void bench_keygen(int iterations) {
    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];

    auto start = Clock::now();
    for (int i = 0; i < iterations; i++) {
        generate_sequencer_keys(pub, sec);
    }
    auto end = Clock::now();
    double ms = elapsed_ms(start, end);
    print_result("Key generation", iterations, ms);
}

// ─── Benchmark 2: Seal (encrypt) ─────────────────────────────────────────────

void bench_seal(const uint8_t pub[crypto_box_PUBLICKEYBYTES], int iterations) {
    const char* msg = "TRADE|BUY|ETH/USDC|1.500000|3200.00|0";
    size_t pt_len   = strlen(msg);
    size_t ct_len   = pt_len + crypto_box_SEALBYTES;
    std::vector<uint8_t> ct(ct_len);

    auto start = Clock::now();
    for (int i = 0; i < iterations; i++) {
        seal_transaction(reinterpret_cast<const uint8_t*>(msg), pt_len, pub, ct.data());
    }
    auto end = Clock::now();
    double ms = elapsed_ms(start, end);
    print_result("seal_transaction (client)", iterations, ms);
}

// ─── Benchmark 3: Single Decrypt ─────────────────────────────────────────────

void bench_decrypt_single(const uint8_t pub[crypto_box_PUBLICKEYBYTES],
                           const uint8_t sec[crypto_box_SECRETKEYBYTES],
                           int iterations)
{
    // Pre-seal one transaction to decrypt repeatedly
    auto ct = make_sealed(pub, 0);
    size_t pt_len = ct.size() - crypto_box_SEALBYTES;

    EncryptedTx enc { ct.data(), ct.size() };
    std::vector<uint8_t> buf(pt_len);
    DecryptedTx dec { buf.data(), 0, 0, 0 };

    auto start = Clock::now();
    for (int i = 0; i < iterations; i++) {
        decrypt_single_tx(&enc, pub, sec, &dec);
    }
    auto end = Clock::now();
    double ms = elapsed_ms(start, end);
    print_result("decrypt_single_tx", iterations, ms);
}

// ─── Benchmark 4: Batch Decrypt (C function) ─────────────────────────────────

void bench_decrypt_batch(const uint8_t pub[crypto_box_PUBLICKEYBYTES],
                          const uint8_t sec[crypto_box_SECRETKEYBYTES],
                          size_t batch_size, int iterations)
{
    // Pre-build a batch of sealed transactions
    std::vector<std::vector<uint8_t>> ciphertexts(batch_size);
    std::vector<std::vector<uint8_t>> recovered(batch_size);
    std::vector<EncryptedTx> enc_arr(batch_size);
    std::vector<DecryptedTx> dec_arr(batch_size);

    for (size_t i = 0; i < batch_size; i++) {
        ciphertexts[i] = make_sealed(pub, (int)i);
        size_t pt_len  = ciphertexts[i].size() - crypto_box_SEALBYTES;
        recovered[i].resize(pt_len);
        enc_arr[i] = { ciphertexts[i].data(), ciphertexts[i].size() };
        dec_arr[i] = { recovered[i].data(), 0, (uint32_t)i, 0 };
    }

    auto start = Clock::now();
    for (int iter = 0; iter < iterations; iter++) {
        decrypt_batch_tx(enc_arr.data(), batch_size, pub, sec, dec_arr.data());
    }
    auto end = Clock::now();
    double ms = elapsed_ms(start, end);

    // Total individual transactions processed
    size_t total_txns = batch_size * (size_t)iterations;

    char label[64];
    snprintf(label, sizeof(label), "decrypt_batch_tx (batch=%zu)", batch_size);
    print_result(label, total_txns, ms,
                 batch_size >= 100 ? "(parallel)" : "");
}

// ─── Benchmark 5: BatchDecryptor class (persistent thread pool) ──────────────

void bench_batch_decryptor_class(const uint8_t pub[crypto_box_PUBLICKEYBYTES],
                                  const uint8_t sec[crypto_box_SECRETKEYBYTES],
                                  size_t batch_size, int iterations)
{
    std::vector<std::vector<uint8_t>> ciphertexts(batch_size);
    std::vector<std::vector<uint8_t>> recovered(batch_size);
    std::vector<EncryptedTx> enc_arr(batch_size);
    std::vector<DecryptedTx> dec_arr(batch_size);

    for (size_t i = 0; i < batch_size; i++) {
        ciphertexts[i] = make_sealed(pub, (int)i);
        size_t pt_len  = ciphertexts[i].size() - crypto_box_SEALBYTES;
        recovered[i].resize(pt_len);
        enc_arr[i] = { ciphertexts[i].data(), ciphertexts[i].size() };
        dec_arr[i] = { recovered[i].data(), 0, (uint32_t)i, 0 };
    }

    // Create the BatchDecryptor once — thread pool persists across iterations
    // This simulates the real-world use case where the server handles many
    // successive batches without respawning threads each time.
    BatchDecryptor bd;

    auto start = Clock::now();
    for (int iter = 0; iter < iterations; iter++) {
        bd.decrypt_batch(enc_arr.data(), batch_size, pub, sec, dec_arr.data());
    }
    auto end = Clock::now();
    double ms = elapsed_ms(start, end);

    size_t total_txns = batch_size * (size_t)iterations;

    char label[64];
    snprintf(label, sizeof(label), "BatchDecryptor (batch=%zu, pool)", batch_size);
    print_result(label, total_txns, ms, "(thread pool reused)");
}

// ─── Benchmark 6: End-to-end single transaction latency ──────────────────────

void bench_e2e_latency(const uint8_t pub[crypto_box_PUBLICKEYBYTES],
                        const uint8_t sec[crypto_box_SECRETKEYBYTES],
                        int iterations)
{
    const char* msg = "TRADE|BUY|ETH/USDC|2.000000|3250.00|0";
    size_t pt_len   = strlen(msg);
    size_t ct_len   = pt_len + crypto_box_SEALBYTES;
    std::vector<uint8_t> ct(ct_len), recovered(pt_len);

    DecryptedTx dec { recovered.data(), 0, 0, 0 };
    EncryptedTx enc { ct.data(), ct_len };

    auto start = Clock::now();
    for (int i = 0; i < iterations; i++) {
        // Seal (user side)
        seal_transaction(reinterpret_cast<const uint8_t*>(msg), pt_len, pub, ct.data());
        // Decrypt (server side)
        decrypt_single_tx(&enc, pub, sec, &dec);
    }
    auto end = Clock::now();
    double ms = elapsed_ms(start, end);
    print_result("E2E: seal + decrypt (1 tx)", iterations, ms, "(full round-trip)");
}

// ─── Main ─────────────────────────────────────────────────────────────────────

int main() {
    if (init_privacy_layer() != SO_SUCCESS) {
        fprintf(stderr, "ERROR: init_privacy_layer() failed\n");
        return 1;
    }

    uint8_t pub[crypto_box_PUBLICKEYBYTES], sec[crypto_box_SECRETKEYBYTES];
    generate_sequencer_keys(pub, sec);

    printf("\n");
    printf("=================================================================\n");
    printf("  Secure-Order :: NaCl Privacy Layer — Performance Benchmarks\n");
    printf("=================================================================\n");
    printf("  %-35s  %12s   %12s\n", "Test", "Throughput", "Latency");
    print_separator();

    // 1. Key generation
    bench_keygen(10000);

    // 2. Seal throughput
    bench_seal(pub, 50000);

    // 3. Single decrypt throughput
    bench_decrypt_single(pub, sec, 50000);

    print_separator();

    // 4. Batch decrypt — various sizes
    bench_decrypt_batch(pub, sec,   1,    5000);
    bench_decrypt_batch(pub, sec,  10,    2000);
    bench_decrypt_batch(pub, sec,  50,     500);
    bench_decrypt_batch(pub, sec, 100,     200);
    bench_decrypt_batch(pub, sec, 500,      50);
    bench_decrypt_batch(pub, sec, 1000,     20);

    print_separator();

    // 5. BatchDecryptor class (persistent pool)
    bench_batch_decryptor_class(pub, sec,  100, 200);
    bench_batch_decryptor_class(pub, sec, 1000,  20);

    print_separator();

    // 6. E2E latency
    bench_e2e_latency(pub, sec, 20000);

    print_separator();
    printf("\n  Target goals:\n");
    printf("    Encryption (seal)    : > 50,000 tx/sec\n");
    printf("    Decryption (single)  : > 50,000 tx/sec\n");
    printf("    Batch 1000 tx/batch  : > 10,000 tx/sec\n");
    printf("\n");

    return 0;
}
