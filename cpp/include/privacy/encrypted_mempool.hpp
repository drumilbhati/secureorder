
#pragma once

#include <cstdint>
#include <deque>
#include <mutex>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <vector>

/**
 * encrypted_mempool
 *
 * Thread-safe storage for encrypted (sealed) transactions waiting to be
 * executed by the sequencer.
 *
 * Design decisions:
 *  - Transactions are stored by a string key (e.g. a UUID or txid) so the
 *    sequencer can look them up individually.
 *  - A separate FIFO deque (arrival_order) records the insertion order so
 *    drain_all() always returns transactions in the exact order they arrived —
 *    this is the core MEV-prevention guarantee.
 *  - All public methods hold a lock for their entire duration, making the
 *    class safe to use from multiple goroutine threads via CGO.
 */
class encrypted_mempool {
public:
    /**
     * add_transaction
     * Stores an encrypted transaction blob under the given key.
     * Also appends the key to the in-order arrival queue.
     * If the key already exists, the value is overwritten but the key is NOT
     * re-appended to the arrival queue (idempotent insertion).
     */
    void add_transaction(const std::string& key, const std::vector<uint8_t>& value);

    /**
     * remove_transaction
     * Removes the transaction blob for the given key.
     * The key remains in arrival_order (it will simply be skipped by drain_all).
     * No-op if the key does not exist.
     */
    void remove_transaction(const std::string& key);

    /**
     * get_transaction
     * Returns a copy of the encrypted bytes for the given key.
     * Throws std::out_of_range if the key does not exist.
     */
    std::vector<uint8_t> get_transaction(const std::string& key);

    /**
     * has_transaction
     * Returns true if the key exists in the mempool (O(1) hash-map lookup).
     */
    bool has_transaction(const std::string& key);

    /**
     * size
     * Returns the number of transactions currently in the mempool.
     */
    size_t size();

    /**
     * drain_all
     * Atomically removes and returns ALL transactions in strict FIFO order
     * (the order they were first added via add_transaction).
     * After this call the mempool is empty.
     *
     * This is the primary method called by the Go sequencer when it is ready
     * to decrypt and execute a batch. Returning a vector of pairs<key, bytes>
     * preserves the txid for the Sequence Commitment hash.
     */
    std::vector<std::pair<std::string, std::vector<uint8_t>>> drain_all();

private:
    std::mutex mtx;
    std::unordered_map<std::string, std::vector<uint8_t>> mempool;

    // Records insertion order — the sequencer MUST process in this order
    // to guarantee FIFO settlement and prevent MEV reordering.
    std::deque<std::string> arrival_order;
};
