import unittest
import os
from pathlib import Path
import pandas as pd
from optimizer.reporter import calculate_metrics_from_trade_log

class TestReporter(unittest.TestCase):

    def setUp(self):
        """Set up for the test."""
        # It's better to create a dummy CSV for the test to make it self-contained
        self.test_data = [
            ['time', 'pair', 'side', 'price', 'size', 'transaction_id', 'is_cancelled', 'is_my_trade'],
            ['2025-08-16 10:00:00+09', 'btc_jpy', 'buy', 17300000, 0.005, 1, 0, 1],
            ['2025-08-16 10:05:00+09', 'btc_jpy', 'sell', 17350000, 0.005, 2, 0, 1], # Profit: 250
            ['2025-08-16 10:10:00+09', 'btc_jpy', 'buy', 17400000, 0.005, 3, 0, 1],
            ['2025-08-16 10:15:00+09', 'btc_jpy', 'sell', 17380000, 0.005, 4, 0, 1], # Loss: -100
            ['2025-08-16 10:20:00+09', 'btc_jpy', 'buy', 17300000, 0.005, 5, 1, 1], # Cancelled
        ]
        self.csv_path = 'test_trades.csv'
        with open(self.csv_path, 'w') as f:
            for row in self.test_data:
                f.write(','.join(map(str, row)) + '\n')

    def tearDown(self):
        """Clean up after the test."""
        if os.path.exists(self.csv_path):
            os.remove(self.csv_path)

    def test_calculate_metrics_from_trade_log(self):
        """
        Test that metrics are calculated correctly from a trade log CSV.
        """
        summary = calculate_metrics_from_trade_log(self.csv_path)

        # --- Assertions ---
        self.assertIsNotNone(summary)
        self.assertIsInstance(summary, dict)

        # Check that key metrics are present and have plausible values
        self.assertEqual(summary['total_trades'], 2)
        self.assertEqual(summary['winning_trades'], 1)
        self.assertEqual(summary['losing_trades'], 1)
        self.assertEqual(summary['win_rate'], 0.5)

        self.assertAlmostEqual(summary['total_pnl'], 150) # 250 - 100

        # Profit factor = total profit / total loss
        self.assertAlmostEqual(summary['profit_factor'], 2.5) # 250 / 100

        # Sharpe ratio should be a non-zero float
        self.assertIsInstance(summary['sharpe_ratio'], float)
        self.assertNotEqual(summary['sharpe_ratio'], 0.0)

        # Max drawdown
        # PnL history: [250, -100]
        # Cumulative: [250, 150]
        # Peak: [250, 250]
        # Drawdown: [0, 100]
        self.assertAlmostEqual(summary['max_drawdown'], 100.0)

if __name__ == '__main__':
    unittest.main()
