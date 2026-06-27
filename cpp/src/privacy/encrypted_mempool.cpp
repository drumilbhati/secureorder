/*
 * encrypted_mempool.cpp — C++ implementation of the encrypted transaction store.
 *
 * This class provides a key-value store where:
 *   - Keys   are transaction identifiers (strings).
 *   - Values are raw ciphertext bytes (std::vector<uint8_t>).
 *
 * FIFO ordering is maintained via a separate arrival_order vector that records
 * keys in insertion order. The hash-map (unordered_map) provides O(1) lookups
 * and updates, while arrival_order preserves the order for drain_all().
 *
 * All public methods acquire a mutex to protect the shared state. The mutex is
 * std::mutex (non-recursive, not reader-writer) because:
 *   - Writes (add, remove) are the common case.
 *   - Reads (get, has, size) are infrequent and fast enough that a full lock is
 *     acceptable without the added complexity of std::shared_mutex.
 *
 * Note: This class is used by the C++ layer only. The Go sequencer uses the
 * Go EncryptedMempool (pkg/sequencing/encrypted_mempool.go) instead.
 */
#include "../../include/privacy/encrypted_mempool.hpp"

#include <mutex>
#include <stdexcept>

/*
 * add_transaction — insert or update a transaction.
 *
 * If the key is new, it is appended to arrival_order to record its FIFO
 * position. If the key already exists, only the value (ciphertext) is
 * updated — the existing position in arrival_order is preserved.
 *
 * This idempotency is useful for retry scenarios: re-submitting the same
 * transaction ID updates its ciphertext (e.g. after re-encryption) without
 * changing its priority in the queue.
 */
void encrypted_mempool::add_transaction(const std::string& key,
                                         const std::vector<uint8_t>& value)
{
    std::lock_guard<std::mutex> lock(mtx);

    // Check if this is a new key before inserting — operator[] would create
    // a default entry if the key is absent, so we must check first.
    bool is_new = (mempool.find(key) == mempool.end());
    mempool[key] = value;
    if (is_new) {
        arrival_order.push_back(key);
    }
}

/*
 * remove_transaction — remove a transaction by key.
 *
 * Erases the key from the hash-map. The key is NOT removed from arrival_order
 * immediately (that would require O(n) vector search and shift). Instead,
 * drain_all() skips keys that are absent from the hash-map, so stale entries
 * in arrival_order are harmless — they are filtered out at drain time.
 */
void encrypted_mempool::remove_transaction(const std::string& key)
{
    std::lock_guard<std::mutex> lock(mtx);
    // erase() is a no-op if the key is not present — safe to call unconditionally.
    mempool.erase(key);
}

/*
 * get_transaction — retrieve the ciphertext for a key.
 *
 * Uses std::unordered_map::at() which throws std::out_of_range if the key
 * is not found. Callers should call has_transaction() first if they are
 * unsure whether the key exists, or catch the exception.
 *
 * Returns a copy of the ciphertext vector (the caller owns the returned data).
 */
std::vector<uint8_t> encrypted_mempool::get_transaction(const std::string& key)
{
    std::lock_guard<std::mutex> lock(mtx);
    // at() throws std::out_of_range on missing key — intentional (caller must check first).
    return mempool.at(key);
}

/*
 * has_transaction — test for key existence.
 * Returns true if the key is currently in the hash-map (not removed).
 */
bool encrypted_mempool::has_transaction(const std::string& key)
{
    std::lock_guard<std::mutex> lock(mtx);
    return mempool.find(key) != mempool.end();
}

/*
 * size — return the number of transactions currently in the mempool.
 * Note: does NOT count stale entries in arrival_order that were removed.
 */
size_t encrypted_mempool::size()
{
    std::lock_guard<std::mutex> lock(mtx);
    return mempool.size();
}

/*
 * drain_all — atomically remove and return all transactions in FIFO order.
 *
 * Walks arrival_order in insertion order (FIFO guarantee). For each key,
 * checks whether it still exists in the hash-map — removed transactions
 * leave stale keys in arrival_order, which are silently skipped here.
 *
 * After this call:
 *   - mempool is empty (clear()).
 *   - arrival_order is empty (clear()).
 *   - The caller owns all returned (key, ciphertext) pairs.
 *
 * This method is called by the Go layer when a reveal batch is ready.
 */
std::vector<std::pair<std::string, std::vector<uint8_t>>>
encrypted_mempool::drain_all()
{
    std::lock_guard<std::mutex> lock(mtx);

    std::vector<std::pair<std::string, std::vector<uint8_t>>> result;
    result.reserve(arrival_order.size());

    // Walk in FIFO insertion order, skipping any removed keys.
    for (const auto& key : arrival_order) {
        auto it = mempool.find(key);
        if (it != mempool.end()) {
            // std::move transfers ownership of the ciphertext vector without copying.
            result.emplace_back(it->first, std::move(it->second));
        }
    }

    // Reset both containers so the mempool is ready for the next batch.
    mempool.clear();
    arrival_order.clear();

    return result;
}
