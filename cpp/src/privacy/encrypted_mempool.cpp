#include "../../include/privacy/encrypted_mempool.hpp"
#include <mutex>
void encrypted_mempool::add_transaction(const std::string& key, const std::vector<uint8_t>& value){
    std::lock_guard<std::mutex> lock(mtx);
    mempool[key] = value;
}

void encrypted_mempool::remove_transaction(const std::string& key){
    std::lock_guard<std::mutex> lock(mtx);
    mempool.erase(key);
}
std::vector<uint8_t> encrypted_mempool::get_transaction(const std::string& key){
    std::lock_guard<std::mutex> lock(mtx);
    return mempool.at(key);
}
