import json
import random
import threading
import time
import uuid

try:
    import matplotlib.pyplot as plt

    MATPLOTLIB_AVAILABLE = True
except ImportError:
    MATPLOTLIB_AVAILABLE = False
    print("Warning: matplotlib is not installed. Visualization will be skipped.")
    print("Install it using: pip install matplotlib")

# Optional: If you want to actually spam the Go sequencer, you would need grpcio and the compiled protos.
# For this simulation, we will model the DEX and the concurrent load, generating the "encrypted" payloads
# that *would* be sent to the sequencer, demonstrating the exact math and ordering.
# To integrate with real gRPC later, uncomment and use grpc.insecure_channel('localhost:12345').


class ConstantProductDEX:
    """
    Simulates the math of MockDEXEngine.sol (x * y = k) locally.
    This helps us verify if the sequencer's final order results in the expected L2 state.
    """

    def __init__(self, reserve_a=100000.0, reserve_b=100000.0):
        self.lock = threading.Lock()
        self.reserve_a = reserve_a
        self.reserve_b = reserve_b
        self.price_history = []
        self._record_price()

    def _record_price(self):
        # Price of Token A in terms of Token B
        if self.reserve_a > 0:
            price = self.reserve_b / self.reserve_a
            self.price_history.append((time.time(), price))

    def add_liquidity(self, amount_a, amount_b):
        with self.lock:
            self.reserve_a += amount_a
            self.reserve_b += amount_b
            self._record_price()
            return True

    def swap_a_for_b(self, amount_a_in):
        with self.lock:
            # 0.3% fee simulation (standard Uniswap V2 / standard DEX)
            amount_in_with_fee = amount_a_in * 0.997
            numerator = amount_in_with_fee * self.reserve_b
            denominator = self.reserve_a + amount_in_with_fee
            amount_b_out = numerator / denominator

            self.reserve_a += amount_a_in
            self.reserve_b -= amount_b_out
            self._record_price()
            return amount_b_out

    def swap_b_for_a(self, amount_b_in):
        with self.lock:
            amount_in_with_fee = amount_b_in * 0.997
            numerator = amount_in_with_fee * self.reserve_a
            denominator = self.reserve_b + amount_in_with_fee
            amount_a_out = numerator / denominator

            self.reserve_b += amount_b_in
            self.reserve_a -= amount_a_out
            self._record_price()
            return amount_a_out

    def get_price_history(self):
        return self.price_history


def encode_transaction(action, **kwargs):
    """
    Mocks the creation of a payload that would be encrypted by the C++ layer
    and sent to the Go sequencer.
    """
    payload = {
        "tx_id": str(uuid.uuid4())[:8],
        "action": action,
        "timestamp": time.time(),
        "params": kwargs,
    }
    # In a real run, this JSON string is encrypted into raw bytes.
    return json.dumps(payload).encode("utf-8")


def simulate_liquidity_provider(dex_model, num_actions=5):
    """LPs occasionally add large chunks of liquidity to stabilize the pool."""
    for _ in range(num_actions):
        time.sleep(random.uniform(0.1, 0.5))
        # Add somewhat balanced liquidity
        amount = random.uniform(5000, 15000)
        dex_model.add_liquidity(amount, amount)
        tx_bytes = encode_transaction("ADD_LIQUIDITY", amount_a=amount, amount_b=amount)
        # TODO: send tx_bytes to Go Sequencer via gRPC
        print(f"[LP] Added Liquidity: {amount:.2f} A / {amount:.2f} B")


def simulate_trader(dex_model, trader_id, num_trades=20):
    """Traders rapidly spam buy/sell orders, causing high concurrency load."""
    for _ in range(num_trades):
        time.sleep(random.uniform(0.01, 0.1))  # Fast trading
        trade_type = random.choice(["SWAP_A_FOR_B", "SWAP_B_FOR_A"])
        amount = random.uniform(10, 500)

        if trade_type == "SWAP_A_FOR_B":
            out = dex_model.swap_a_for_b(amount)
            print(f"[Trader {trader_id}] Swapped {amount:.2f} A for {out:.2f} B")
        else:
            out = dex_model.swap_b_for_a(amount)
            print(f"[Trader {trader_id}] Swapped {amount:.2f} B for {out:.2f} A")

        tx_bytes = encode_transaction(trade_type, amount_in=amount)
        # TODO: send tx_bytes to Go Sequencer via gRPC


def plot_results(dex_model):
    """Visualizes the price impact over time to prove the DEX logic handled the load."""
    if not MATPLOTLIB_AVAILABLE:
        return

    history = dex_model.get_price_history()
    if not history:
        return

    times, prices = zip(*history)
    # Normalize times to start at 0
    start_time = times[0]
    relative_times = [t - start_time for t in times]

    plt.figure(figsize=(10, 5))
    plt.plot(relative_times, prices, marker="o", linestyle="-", markersize=3, alpha=0.7)
    plt.title("DEX Simulation: Token A Price Impact Under High Load")
    plt.xlabel("Time (seconds)")
    plt.ylabel("Price of Token A (in Token B)")
    plt.grid(True)
    plt.tight_layout()

    output_file = "dex_stress_test_chart.png"
    plt.savefig(output_file)
    print(f"\n[+] Visualization saved to {output_file}")
    plt.show()


if __name__ == "__main__":
    print("========================================")
    print("   SECURE-ORDER DEX STRESS TEST MODEL   ")
    print("========================================")

    dex = ConstantProductDEX(reserve_a=100000, reserve_b=100000)

    print(f"Initial Pool State: {dex.reserve_a} A / {dex.reserve_b} B")
    print("Starting simulation with concurrent LPs and Traders...\n")

    threads = []

    # 2 Liquidity Providers
    for i in range(2):
        t = threading.Thread(target=simulate_liquidity_provider, args=(dex, 5))
        threads.append(t)

    # 10 Concurrent Traders spamming the system
    for i in range(10):
        t = threading.Thread(target=simulate_trader, args=(dex, i, 15))
        threads.append(t)

    # Start the firehose
    start_time = time.time()
    for t in threads:
        t.start()

    # Wait for all simulated actors to finish
    for t in threads:
        t.join()

    print("\n========================================")
    print("             SIMULATION COMPLETE          ")
    print("========================================")
    print(f"Time Elapsed: {time.time() - start_time:.2f} seconds")
    print(f"Final Pool State: {dex.reserve_a:.2f} A / {dex.reserve_b:.2f} B")
    print(f"Total Transactions Processed: {len(dex.price_history) - 1}")

    # Generate the chart
    plot_results(dex)
