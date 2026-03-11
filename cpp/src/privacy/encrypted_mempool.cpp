#include "../../include/privacy/encrypted_mempool.hpp"

#include <mutex>
#include <stdexcept>

void encrypted_mempool::add_transaction(const std::string& key,
                                         const std::vector<uint8_t>& value)
{
    std::lock_guard<std::mutex> lock(mtx);

    // Only append to the arrival queue if this is a NEW key.
    // Re-inserting an existing key updates the value but preserves
    // the original position in the FIFO order (idempotent insert).
    bool is_new = (mempool.find(key) == mempool.end());
    mempool[key] = value;
    if (is_new) {
        arrival_order.push_back(key);
    }
}

void encrypted_mempool::remove_transaction(const std::string& key)
{
    std::lock_guard<std::mutex> lock(mtx);
    // Remove from the hash-map. The key stays in arrival_order but
    // drain_all() will skip keys that are no longer in the hash-map.
    mempool.erase(key);
}

std::vector<uint8_t> encrypted_mempool::get_transaction(const std::string& key)
{
    std::lock_guard<std::mutex> lock(mtx);
    // at() throws std::out_of_range if key not found — intentional,
    // callers must check has_transaction() first if unsure.
    return mempool.at(key);
}

bool encrypted_mempool::has_transaction(const std::string& key)
{
    std::lock_guard<std::mutex> lock(mtx);
    return mempool.find(key) != mempool.end();
}

size_t encrypted_mempool::size()
{
    std::lock_guard<std::mutex> lock(mtx);
    return mempool.size();
}

std::vector<std::pair<std::string, std::vector<uint8_t>>>
encrypted_mempool::drain_all()
{
    std::lock_guard<std::mutex> lock(mtx);

    std::vector<std::pair<std::string, std::vector<uint8_t>>> result;
    result.reserve(arrival_order.size());

    // Walk the arrival queue in insertion order (FIFO guarantee).
    // Skip keys that were removed after insertion.
    for (const auto& key : arrival_order) {
        auto it = mempool.find(key);
        if (it != mempool.end()) {
            result.emplace_back(it->first, std::move(it->second));
        }
    }

    // Clear everything so the mempool is ready for the next batch.
    mempool.clear();
    arrival_order.clear();

    return result;
}
