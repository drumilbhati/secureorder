#include "../../include/privacy/thread_pool.h"
#include <functional>
#include <mutex>
#include <thread>

ThreadPool::ThreadPool(size_t threads) : stop(false) {
    // Fallback: minimum 2 threads in the pool at startup
    if (threads == 0) threads = 2;

    for (size_t i = 0; i < threads; i++) {
        workers.emplace_back(&ThreadPool::worker_thread, this);
    }
}

void ThreadPool::worker_thread() {
    while (true) {
        std::function<void()> task;

        {
            std::unique_lock<std::mutex> lock(queue_mutex);

            // Sleep until there is work to do or the pool is shutting down
            condition.wait(lock, [this] {
                return stop.load() || !tasks.empty();
            });

            // Pool is stopping and no tasks remain — exit the thread cleanly
            if (stop.load() && tasks.empty()) return;

            task = std::move(tasks.front());
            tasks.pop();
        }

        // Execute the decryption task outside the lock so other threads
        // can pick up tasks concurrently
        task();

        // Hold queue_mutex while decrementing active_tasks and notifying
        // wait_all(). This closes the race window where:
        //   1. active_tasks hits 0 and notify_all() fires
        //   2. wait_all() hasn't started waiting yet
        //   3. wait_all() then waits forever because the notify was missed
        // By holding the same mutex that wait_all() acquires, we guarantee
        // the notification is never lost.
        {
            std::unique_lock<std::mutex> lock(queue_mutex);
            if (--active_tasks == 0) {
                wait_condition.notify_all();
            }
        }
    }
}

void ThreadPool::submit(std::function<void()> task) {
    {
        std::unique_lock<std::mutex> lock(queue_mutex);
        tasks.emplace(std::move(task));
        active_tasks++;
    }
    // Wake one idle worker to pick up the new task
    condition.notify_one();
}

void ThreadPool::wait_all() {
    std::unique_lock<std::mutex> lock(queue_mutex);
    // Wait until every submitted task has been fully executed.
    // The predicate re-checks under the lock, so if all tasks already
    // finished before we got here we return immediately without blocking.
    wait_condition.wait(lock, [this]() {
        return active_tasks == 0 && tasks.empty();
    });
}

ThreadPool::~ThreadPool() {
    {
        std::unique_lock<std::mutex> lock(queue_mutex);
        stop.store(true);
    }
    // Wake all workers so they can observe stop == true and exit
    condition.notify_all();

    for (std::thread &worker : workers) {
        worker.join();
    }
}