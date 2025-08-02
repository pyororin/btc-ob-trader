import unittest
from .utils import nest_params

class TestUtils(unittest.TestCase):

    def test_nest_params(self):
        """
        Tests that a flat parameter dictionary from Optuna is correctly
        converted into a nested dictionary.
        """
        flat_params = {
            'spread_limit': 50,
            'lot_max_ratio': 0.5,
            'long_tp': 150,
            'long_sl': -150,
            'adaptive_position_sizing_enabled': True,
            'adaptive_num_trades': 5,
            'signal_composite_threshold': 1.2, # This is an old key format, should be handled
            'composite_threshold': 1.3, # This is the new key format for signal
            'slope_filter_enabled': False,
            'slope_period': 20,
            'ewma_lambda': 0.15
        }

        expected_nested_params = {
            'spread_limit': 50,
            'lot_max_ratio': 0.5,
            'long': {
                'tp': 150,
                'sl': -150,
                'obi_threshold': 0.0
            },
            'adaptive_position_sizing': {
                'enabled': True,
                'num_trades': 5
            },
            'signal': {
                'composite_threshold': 1.3,
                'slope_filter': {
                    'enabled': False,
                    'period': 20
                }
            },
            'volatility': {
                'ewma_lambda': 0.15,
                'dynamic_obi': {}
            },
            # Ensure other keys exist even if not in flat_params
            'short': {'obi_threshold': 0.0},
            'twap': {},
            'risk': {}
        }

        nested_params = nest_params(flat_params)

        # Using direct dict comparison which is strict
        self.maxDiff = None # Show full diff on failure
        self.assertDictEqual(nested_params, expected_nested_params)

    def test_nest_params_all_keys(self):
        """ Tests with a more complete set of flat parameters. """
        flat_params = {
            'spread_limit': 80,
            'lot_max_ratio': 0.9,
            'order_ratio': 0.9,
            'adaptive_position_sizing_enabled': True,
            'adaptive_num_trades': 10,
            'adaptive_reduction_step': 0.8,
            'adaptive_min_ratio': 0.4,
            'long_tp': 100,
            'long_sl': -100,
            'short_tp': 100,
            'short_sl': -100,
            'hold_duration_ms': 500,
            'obi_weight': 1.5,
            'ofi_weight': 1.5,
            'cvd_weight': 1.5,
            'micro_price_weight': 1.5,
            'composite_threshold': 1.0,
            'slope_filter_enabled': False,
            'slope_period': 10,
            'slope_threshold': 0.3,
            'ewma_lambda': 0.2,
            'dynamic_obi_enabled': True,
            'volatility_factor': 3.0,
            'min_threshold_factor': 0.7,
            'max_threshold_factor': 2.0,
            'twap_enabled': True,
            'twap_max_order_size_btc': 0.05,
            'twap_interval_seconds': 5,
            'twap_partial_exit_enabled': False,
            'twap_profit_threshold': 0.5,
            'twap_exit_ratio': 0.5,
            'risk_max_drawdown_percent': 20,
            'risk_max_position_ratio': 0.8
        }

        nested = nest_params(flat_params)

        self.assertTrue(nested['adaptive_position_sizing']['enabled'])
        self.assertEqual(nested['long']['tp'], 100)
        self.assertEqual(nested['signal']['slope_filter']['enabled'], False)
        self.assertEqual(nested['risk']['max_position_ratio'], 0.8)
        self.assertEqual(nested['twap']['exit_ratio'], 0.5)


if __name__ == '__main__':
    unittest.main()
