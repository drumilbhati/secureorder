
#pragma once

#include <iostream>
#include <mutex>
#include <unordered_map>
#include <vector>
class encrypted_mempool{
    private:
    std::mutex mtx;
    std::unordered_map<std::string, std::vector<uint8_t>> mempool;
    public:
    void add_transaction(const std::string& key, const std::vector<uint8_t>& value);
    void remove_transaction(const std::string& key);
    std::vector<uint8_t> get_transaction(const std::string& key);
};
