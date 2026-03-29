import random
import statistics
import threading
import time
import uuid
from concurrent.futures import ThreadPoolExecutor


class OrderLifecycle:
    """Tracks the exact timestamps of a single order as it moves through the architecture."""

    def __init__(self, order_id):
        self.order_id = order_id
        self.sent_at = 0.0
        self.arrived_at = 0.0
        self.committed_at = 0.0
        self.decrypted_at = 0.0
        self.settled_at = 0.0

    def total_latency(self):
        if self.sent_at == 0 or self.settled_at == 0:
            return 0
        return self.settled_at - self.sent_at

    def sequencing_latency(self):
        if self.arrived_at == 0 or self.committed_at == 0:
            return 0
        return self.committed_at - self.arrived_at

    def decryption_latency(self):
        if self.committed_at == 0 or self.decrypted_at == 0:
            return 0
        return self.decrypted_at - self.committed_at

    def execution_latency(self):
        if self.decrypted_at == 0 or self.settled_at == 0:
            return 0
        return self.settled_at - self.decrypted_at


class MetricsRegistry:
    """Safely collects lifecycle data across highly concurrent client loads."""

    def __init__(self):
        self.lock = threading.Lock()
        self.records = {}

    def _get_or_init(self, order_id):
        with self.lock:
            if order_id not in self.records:
                self.records[order_id] = OrderLifecycle(order_id)
            return self.records[order_id]

    def record_sent(self, order_id, timestamp):
        self._get_or_init(order_id).sent_at = timestamp

    def record_arrived(self, order_id, timestamp):
        self._get_or_init(order_id).arrived_at = timestamp

    def record_committed(self, order_id, timestamp):
        self._get_or_init(order_id).committed_at = timestamp

    def record_decrypted(self, order_id, timestamp):
        self._get_or_init(order_id).decrypted_at = timestamp

    def record_settled(self, order_id, timestamp):
        self._get_or_init(order_id).settled_at = timestamp

    def generate_report(self):
        with self.lock:
            latencies = []
            seq_lats = []
            dec_lats = []
            exe_lats = []

            for record in self.records.values():
                tot = record.total_latency()
                if tot > 0:
                    latencies.append(tot)
                    seq_lats.append(record.sequencing_latency())
                    dec_lats.append(record.decryption_latency())
                    exe_lats.append(record.execution_latency())

        total_orders = len(latencies)
        if total_orders == 0:
            return None

        def format_ms(seconds):
            return f"{seconds * 1000:.2f} ms"

        report = {
            "Total Orders": total_orders,
            "Min Latency": format_ms(min(latencies)),
            "Max Latency": format_ms(max(latencies)),
            "Average Latency": format_ms(statistics.mean(latencies)),
            "Median Latency": format_ms(statistics.median(latencies)),
            "P95 Latency": format_ms(statistics.quantiles(latencies, n=100)[94]),
            "P99 Latency": format_ms(statistics.quantiles(latencies, n=100)[98]),
            "Avg Sequencing Time (Go)": format_ms(statistics.mean(seq_lats)),
            "Avg Decryption Time (C++)": format_ms(statistics.mean(dec_lats)),
            "Avg Execution Time (L2)": format_ms(statistics.mean(exe_lats)),
        }
        return report

    def print_report(self):
        report = self.generate_report()
        if not report:
            print("No complete orders to report.")
            return

        print("==================================================")
        print("       SECURE-ORDER ARCHITECTURE EFFICIENCY       ")
        print("==================================================")
        for key, value in report.items():
            print(f"{key:<28}: {value}")
        print("==================================================")


def simulate_architecture_pipeline(registry, order_id):
    """Simulates the lifecycle of a single transaction through the SecureOrder architecture."""

    # 1. Client sends the transaction (Network latency to server)
    registry.record_sent(order_id, time.time())
    time.sleep(random.uniform(0.01, 0.05))

    # 2. Transaction arrives at Go Sequencer (FIFO + Hash Generation)
    registry.record_arrived(order_id, time.time())
    time.sleep(random.uniform(0.001, 0.005))  # Fast Go sequencing

    # 3. Batch is committed to L1/L2 Merkle Tree
    registry.record_committed(order_id, time.time())
    time.sleep(random.uniform(0.02, 0.08))  # Contract state write wait

    # 4. C++ Privacy Layer decrypts the sealed box
    registry.record_decrypted(order_id, time.time())
    time.sleep(random.uniform(0.0005, 0.002))  # Extremely fast parallel decryption

    # 5. Order is executed and settled on the DEX Smart Contract
    time.sleep(random.uniform(0.1, 0.3))  # L2 execution/consensus time
    registry.record_settled(order_id, time.time())


if __name__ == "__main__":
    print("Initializing metrics registry...")
    registry = MetricsRegistry()

    num_orders = 1000
    print(f"Simulating {num_orders} highly concurrent transactions...")

    start_time = time.time()

    # Use thread pool to simulate concurrent users hammering the sequencer
    with ThreadPoolExecutor(max_workers=50) as executor:
        futures = []
        for _ in range(num_orders):
            order_id = str(uuid.uuid4())
            futures.append(
                executor.submit(simulate_architecture_pipeline, registry, order_id)
            )

        # Wait for all simulated pipelines to finish
        for future in futures:
            future.result()

    total_sim_time = time.time() - start_time
    print(f"\nSimulation completed in {total_sim_time:.2f} seconds.\n")

    # Generate and print the final project report
    registry.print_report()
