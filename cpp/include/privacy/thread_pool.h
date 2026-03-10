#ifndef THREAD_POOL_H
#define THREAD_POOL_H

#include <atomic>
#include <condition_variable>
#include <cstddef>
#include <functional>
#include <mutex>
#include <queue>
#include <thread>
#include <vector>

class ThreadPool {
public:
    // Initialise pool with N worker threads.
    // Defaults to the number of hardware cores.
    // Falls back to 2 if hardware_concurrency() returns 0.
    ThreadPool(size_t threads = std::thread::hardware_concurrency());

    // Gracefully shuts down: waits for all in-flight tasks to finish,
    // then joins every worker thread.
    ~ThreadPool();

    // Push a task onto the queue and wake one idle worker.
    void submit(std::function<void()> task);

    // Block the calling thread until every task currently in the queue
    // (and any task being actively executed) has completed.
    void wait_all();

private:
    std::vector<std::thread>          workers;
    std::queue<std::function<void()>> tasks;

    std::mutex              queue_mutex;
    std::condition_variable condition;      // wakes workers when a task arrives
    std::condition_variable wait_condition; // wakes wait_all() when active_tasks hits 0

    // Counts tasks that have been popped from the queue but not yet finished.
    // Using atomic so worker threads can decrement it without holding queue_mutex.
    std::atomic<size_t> active_tasks{0};

    // Changed from plain bool to std::atomic<bool> to prevent a data race:
    // worker threads read `stop` inside their loop without holding queue_mutex,
    // so the write in ~ThreadPool() would be a race under the C++ memory model
    // if this were a plain bool.
    std::atomic<bool> stop{false};

    void worker_thread();
};

#endif // THREAD_POOL_H