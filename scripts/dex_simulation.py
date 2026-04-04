import json
import random
import threading
import time
import uuid

try:
    import grpc

    GRPC_AVAILABLE = True
except ImportError:
    GRPC_AVAILABLE = False

try:
    import matplotlib.pyplot as plt

    MATPLOTLIB_AVAILABLE = True
except ImportError:
    MATPLOTLIB_AVAILABLE = False
    print("Warning: matplotlib is not installed. Visualization will be skipped.")
    print("Install it using: pip install matplotlib")

try:
    import rpc_pb2
    import rpc_pb2_grpc

    RPC_PROTO_AVAILABLE = True
except ImportError:
    RPC_PROTO_AVAILABLE = False


class ConstantProductDEX:
    """
    Simulates the math of MockDEXEngine.sol (x * y = k) locally.
    This helps us verify if the sequencer's final order results in the expected L2 state.
    """

    def __init__(self, reserve_a=100000.0, reserve_b=100000.0):
        self.lock = threading.RLock()
        self.reserve_a = reserve_a
        self.reserve_b = reserve_b
        self.trader_token_a = {}
        self.trader_token_b = {}
        self.fee_vault_token_a = 0.0
        self.fee_vault_token_b = 0.0
        self.settlement_log = []
        self.price_history = []
        self._record_price()

    def ensure_trader(self, trader_id, token_a=10000.0, token_b=10000.0):
        with self.lock:
            if trader_id not in self.trader_token_a:
                self.trader_token_a[trader_id] = token_a
            if trader_id not in self.trader_token_b:
                self.trader_token_b[trader_id] = token_b

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

    def execute_atomic_swap(
        self,
        trader_id,
        trade_type,
        amount_in,
        min_amount_out=0.0,
        inject_failure=False,
    ):
        """
        Atomically executes both legs of settlement in one commit:
        1) swap reserve movement, 2) fee capture.
        If any validation fails, no state is mutated.
        """
        with self.lock:
            self.ensure_trader(trader_id)

            if amount_in <= 0:
                raise ValueError("amount_in must be positive")
            if trade_type not in ("SWAP_A_FOR_B", "SWAP_B_FOR_A"):
                raise ValueError("unsupported trade type")

            if trade_type == "SWAP_A_FOR_B":
                if self.trader_token_a[trader_id] < amount_in:
                    raise ValueError("insufficient TokenA balance")

                fee = amount_in * 0.003
                amount_in_after_fee = amount_in - fee
                amount_out = (amount_in_after_fee * self.reserve_b) / (
                    self.reserve_a + amount_in_after_fee
                )

                if amount_out < min_amount_out:
                    raise ValueError("slippage too high")
                if amount_out <= 0 or amount_out > self.reserve_b:
                    raise ValueError("insufficient pool TokenB liquidity")

                # Stage all values first; commit only if everything is valid.
                new_trader_a = self.trader_token_a[trader_id] - amount_in
                new_trader_b = self.trader_token_b[trader_id] + amount_out
                new_reserve_a = self.reserve_a + amount_in_after_fee
                new_reserve_b = self.reserve_b - amount_out
                new_fee_vault_a = self.fee_vault_token_a + fee

                if inject_failure:
                    raise RuntimeError("injected failure before commit")

                self.trader_token_a[trader_id] = new_trader_a
                self.trader_token_b[trader_id] = new_trader_b
                self.reserve_a = new_reserve_a
                self.reserve_b = new_reserve_b
                self.fee_vault_token_a = new_fee_vault_a

            else:
                if self.trader_token_b[trader_id] < amount_in:
                    raise ValueError("insufficient TokenB balance")

                fee = amount_in * 0.003
                amount_in_after_fee = amount_in - fee
                amount_out = (amount_in_after_fee * self.reserve_a) / (
                    self.reserve_b + amount_in_after_fee
                )

                if amount_out < min_amount_out:
                    raise ValueError("slippage too high")
                if amount_out <= 0 or amount_out > self.reserve_a:
                    raise ValueError("insufficient pool TokenA liquidity")

                # Stage all values first; commit only if everything is valid.
                new_trader_b = self.trader_token_b[trader_id] - amount_in
                new_trader_a = self.trader_token_a[trader_id] + amount_out
                new_reserve_b = self.reserve_b + amount_in_after_fee
                new_reserve_a = self.reserve_a - amount_out
                new_fee_vault_b = self.fee_vault_token_b + fee

                if inject_failure:
                    raise RuntimeError("injected failure before commit")

                self.trader_token_b[trader_id] = new_trader_b
                self.trader_token_a[trader_id] = new_trader_a
                self.reserve_b = new_reserve_b
                self.reserve_a = new_reserve_a
                self.fee_vault_token_b = new_fee_vault_b

            self._record_price()
            receipt = {
                "trader_id": trader_id,
                "trade_type": trade_type,
                "amount_in": amount_in,
                "amount_out": amount_out,
                "timestamp": time.time(),
            }
            self.settlement_log.append(receipt)
            return receipt

    def get_price_history(self):
        return self.price_history


def encode_transaction(action, **kwargs):
    """
    Creates a payload and encodes it into bytes.
    In the real C++ integration, this payload would be strongly encrypted.
    Here we simulate the raw sealed-box ciphertext going over the network.
    """
    payload = {
        "tx_id": str(uuid.uuid4())[:8],
        "action": action,
        "timestamp": time.time(),
        "params": kwargs,
    }
    return json.dumps(payload).encode("utf-8")


def simulate_liquidity_provider(dex_model, stub, num_actions=5):
    """LPs occasionally add large chunks of liquidity to stabilize the pool."""
    for _ in range(num_actions):
        time.sleep(random.uniform(0.1, 0.5))
        amount = random.uniform(5000, 15000)
        dex_model.add_liquidity(amount, amount)

        tx_bytes = encode_transaction("ADD_LIQUIDITY", amount_a=amount, amount_b=amount)

        # Send to actual Go Sequencer via gRPC
        if not (GRPC_AVAILABLE and RPC_PROTO_AVAILABLE):
            continue
        try:
            req = rpc_pb2.SubmitRequest(ciphertext=tx_bytes)
            stub.SubmitTx(req)
            print(f"[LP] Sent Add Liquidity: {amount:.2f} A / {amount:.2f} B")
        except grpc.RpcError as e:
            print(f"[LP] gRPC Error: {e.code()}")


def simulate_trader(dex_model, trader_id, stub, num_trades=20):
    """Traders rapidly spam buy/sell orders, causing high concurrency load."""
    for _ in range(num_trades):
        time.sleep(random.uniform(0.01, 0.1))  # Fast trading
        trade_type = random.choice(["SWAP_A_FOR_B", "SWAP_B_FOR_A"])
        amount = random.uniform(10, 500)

        trader_key = f"trader-{trader_id}"

        # Atomic local settlement model: swap + fee move together or fail together.
        try:
            receipt = dex_model.execute_atomic_swap(trader_key, trade_type, amount)
            if trade_type == "SWAP_A_FOR_B":
                print(
                    f"[Trader {trader_id}] Settled Swap: {amount:.2f} A for {receipt['amount_out']:.2f} B"
                )
            else:
                print(
                    f"[Trader {trader_id}] Settled Swap: {amount:.2f} B for {receipt['amount_out']:.2f} A"
                )
        except Exception as e:
            print(f"[Trader {trader_id}] Settlement rejected: {e}")
            continue

        tx_bytes = encode_transaction(trade_type, amount_in=amount)

        # Send to actual Go Sequencer via gRPC
        if not (GRPC_AVAILABLE and RPC_PROTO_AVAILABLE):
            continue
        try:
            req = rpc_pb2.SubmitRequest(ciphertext=tx_bytes)
            stub.SubmitTx(req)
        except grpc.RpcError as e:
            print(f"[Trader {trader_id}] gRPC Error: {e.code()}")


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
    plt.title("DEX Stress Test: Token A Price Impact & Sequencer Load")
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
    print("   SECURE-ORDER DEX STRESS TEST (gRPC)  ")
    print("========================================")

    if not GRPC_AVAILABLE:
        print("Error: grpc is not installed. Install with: pip install grpcio")
        exit(1)

    if not RPC_PROTO_AVAILABLE:
        print("Error: Could not import generated gRPC files.")
        print("Please run the following command in the scripts directory first:")
        print(
            "python -m grpc_tools.protoc -I../proto --python_out=. --grpc_python_out=. ../proto/rpc.proto"
        )
        exit(1)

    # Establish connection to the Go Sequencer running locally
    print("Connecting to Go Sequencer at localhost:12345...")
    channel = grpc.insecure_channel("localhost:12345")
    stub = rpc_pb2_grpc.RPCServiceStub(channel)

    dex = ConstantProductDEX(reserve_a=100000, reserve_b=100000)

    for i in range(20):
        dex.ensure_trader(f"trader-{i}")

    print(f"Initial Pool State: {dex.reserve_a} A / {dex.reserve_b} B")
    print(
        "Starting simulation with concurrent LPs and Traders hitting the gRPC server...\n"
    )

    threads = []

    # 2 Liquidity Providers
    for i in range(2):
        t = threading.Thread(target=simulate_liquidity_provider, args=(dex, stub, 5))
        threads.append(t)

    # 20 Concurrent Traders spamming the system
    for i in range(20):
        t = threading.Thread(target=simulate_trader, args=(dex, i, stub, 15))
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
