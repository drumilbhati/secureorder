/*
 * thread_pool.cpp — fixed-size, reusable thread pool with a completion barrier.
 *
 * Design goals:
 *   - Zero thread-spawn overhead per batch: threads are created once in the
 *     constructor and live for the lifetime of the pool.
 *   - Correct wait_all() semantics: callers can block until all submitted
 *     tasks have finished executing — not just been dequeued.
 *   - Clean shutdown: destructor drains remaining tasks and joins all workers.
 *
 * Synchronisation model:
 *   - queue_mutex protects tasks, active_tasks, and the stop flag.
 *   - condition wakes sleeping workers when a new task is enqueued or on stop.
 *   - wait_condition is notified by worker threads when active_tasks reaches 0.
 *     It uses the same queue_mutex as the task queue to close the race window
 *     between a task completing and wait_all() entering its wait:
 *
 *       Without the shared mutex, the following race is possible:
 *         1. Worker decrements active_tasks to 0 and calls notify_all().
 *         2. wait_all() hasn't called wait() yet.
 *         3. wait_all() calls wait() and sleeps forever — notification was missed.
 *       By holding queue_mutex during notify_all() in the worker, and acquiring
 *       queue_mutex in wait_all() before checking the predicate, this race is
 *       impossible: either wait_all() holds the lock (worker's notify will be
 *       picked up on the next check), or the worker holds the lock (wait_all()
 *       will see active_tasks == 0 in the predicate before blocking).
 */
#include "../../include/privacy/thread_pool.h"
#include <functional>
#include <mutex>
#include <thread>

/*
 * Constructor — spawns 'threads' worker threads immediately.
 * If threads == 0 (e.g. hardware_concurrency returned 0 on an unusual platform),
 * we fall back to 2 threads so the pool is always functional.
 */
ThreadPool::ThreadPool(size_t threads) : stop(false) {
    if (threads == 0) threads = 2;

    for (size_t i = 0; i < threads; i++) {
        workers.emplace_back(&ThreadPool::worker_thread, this);
    }
}

/*
 * worker_thread — the body of each pool worker.
 *
 * Each worker loops forever:
 *   1. Waits on `condition` until there is a task or the pool is stopping.
 *   2. Pops one task from the front of the queue.
 *   3. Releases the lock and executes the task.
 *   4. After the task finishes, decrements active_tasks under the lock.
 *      If active_tasks reaches 0, notifies wait_all().
 */
void ThreadPool::worker_thread() {
    while (true) {
        std::function<void()> task;

        {
            std::unique_lock<std::mutex> lock(queue_mutex);

            // Sleep until a task is available OR the pool is shutting down.
            // The lambda re-checks under the lock to avoid spurious wakeups.
            condition.wait(lock, [this] {
                return stop.load() || !tasks.empty();
            });

            // If the pool is stopping and there are no more tasks, exit cleanly.
            if (stop.load() && tasks.empty()) return;

            // Pop the next task. Move semantics avoid a copy of the std::function.
            task = std::move(tasks.front());
            tasks.pop();
        } // Release queue_mutex before executing the task.

        // Execute the decryption chunk outside the lock so other workers
        // can concurrently pick up and execute their own tasks.
        task();

        // Decrement active_tasks under queue_mutex. This is the key correctness
        // requirement: by holding queue_mutex here, we close the race window
        // where wait_all() could miss the notification (see module comment above).
        {
            std::unique_lock<std::mutex> lock(queue_mutex);
            if (--active_tasks == 0) {
                wait_condition.notify_all(); // wake any wait_all() callers
            }
        }
    }
}

/*
 * submit — enqueue a task and wake one idle worker.
 *
 * Increments active_tasks under the lock before releasing it, so the count
 * is always >= the number of in-flight tasks. active_tasks reaching 0 is
 * therefore a reliable signal that all submitted tasks are complete.
 */
void ThreadPool::submit(std::function<void()> task) {
    {
        std::unique_lock<std::mutex> lock(queue_mutex);
        tasks.emplace(std::move(task));
        active_tasks++;
    }
    // Notify one worker outside the lock to reduce lock contention.
    condition.notify_one();
}

/*
 * wait_all — block until every previously submitted task has finished.
 *
 * Acquires queue_mutex and waits on wait_condition with the predicate
 * (active_tasks == 0 && tasks.empty()). The dual check is defensive: it
 * handles the unlikely case where a task completes and active_tasks reaches 0
 * before a concurrent submit() has pushed the next task.
 *
 * If all tasks already finished before wait_all() is called, the predicate
 * is immediately true and the function returns without blocking.
 */
void ThreadPool::wait_all() {
    std::unique_lock<std::mutex> lock(queue_mutex);
    wait_condition.wait(lock, [this]() {
        return active_tasks == 0 && tasks.empty();
    });
}

/*
 * Destructor — graceful shutdown.
 *
 * Sets stop = true under the lock, then wakes all workers. Each worker's
 * condition.wait() predicate includes stop, so all idle workers wake up,
 * observe stop == true with an empty queue, and exit their loop cleanly.
 * We then join() every thread to ensure they have fully exited before the
 * ThreadPool object is destroyed and its members are freed.
 */
ThreadPool::~ThreadPool() {
    {
        std::unique_lock<std::mutex> lock(queue_mutex);
        stop.store(true);
    }
    condition.notify_all(); // wake all sleeping workers
    for (std::thread &worker : workers) {
        worker.join();
    }
}
