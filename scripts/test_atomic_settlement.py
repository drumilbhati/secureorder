import copy
import os
import sys
import unittest

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
if SCRIPT_DIR not in sys.path:
    sys.path.insert(0, SCRIPT_DIR)

from dex_simulation import ConstantProductDEX


class AtomicSettlementTests(unittest.TestCase):
    def setUp(self):
        self.dex = ConstantProductDEX(reserve_a=100000.0, reserve_b=100000.0)
        self.trader = "alice"
        self.dex.ensure_trader(self.trader, token_a=5000.0, token_b=5000.0)

    def _snapshot(self):
        return {
            "reserve_a": self.dex.reserve_a,
            "reserve_b": self.dex.reserve_b,
            "trader_a": self.dex.trader_token_a[self.trader],
            "trader_b": self.dex.trader_token_b[self.trader],
            "fee_a": self.dex.fee_vault_token_a,
            "fee_b": self.dex.fee_vault_token_b,
            "settlements": copy.deepcopy(self.dex.settlement_log),
        }

    def test_successful_atomic_settlement_updates_swap_and_fee_together(self):
        before = self._snapshot()

        receipt = self.dex.execute_atomic_swap(self.trader, "SWAP_A_FOR_B", 100.0)

        self.assertGreater(receipt["amount_out"], 0)
        self.assertLess(self.dex.trader_token_a[self.trader], before["trader_a"])
        self.assertGreater(self.dex.trader_token_b[self.trader], before["trader_b"])
        self.assertGreater(self.dex.fee_vault_token_a, before["fee_a"])
        self.assertGreater(self.dex.reserve_a, before["reserve_a"])
        self.assertLess(self.dex.reserve_b, before["reserve_b"])
        self.assertEqual(len(self.dex.settlement_log), len(before["settlements"]) + 1)

    def test_injected_failure_rolls_back_everything(self):
        before = self._snapshot()

        with self.assertRaises(RuntimeError):
            self.dex.execute_atomic_swap(
                self.trader,
                "SWAP_A_FOR_B",
                100.0,
                inject_failure=True,
            )

        after = self._snapshot()
        self.assertEqual(after, before)

    def test_validation_failure_prevents_partial_state_changes(self):
        before = self._snapshot()

        with self.assertRaises(ValueError):
            self.dex.execute_atomic_swap(self.trader, "SWAP_A_FOR_B", 1_000_000.0)

        after = self._snapshot()
        self.assertEqual(after, before)


if __name__ == "__main__":
    unittest.main()
