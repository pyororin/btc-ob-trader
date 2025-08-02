import unittest
from .utils import nest_params

class TestUtils(unittest.TestCase):

    def test_nest_params_fully(self):
        """
        Tests that a complete flat parameter dictionary from Optuna is
        correctly and exactly converted into a nested dictionary.
        """
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
            'short_tp': 110,
            'short_sl': -110,
            'hold_duration_ms': 500,
            'obi_weight': 1.5,
            'ofi_weight': 1.4,
            'cvd_weight': 1.3,
            'micro_price_weight': 1.2,
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

        expected_nested_params = {
            'spread_limit': 80,
            'lot_max_ratio': 0.9,
            'order_ratio': 0.9,
            'adaptive_position_sizing': {
                'enabled': True,
                'num_trades': 10,
                'reduction_step': 0.8,
                'min_ratio': 0.4
            },
            'long': {
                'obi_threshold': 0.0,
                'tp': 100,
                'sl': -100
            },
            'short': {
                'obi_threshold': 0.0,
                'tp': 110,
                'sl': -110
            },
            'signal': {
                'hold_duration_ms': 500,
                'obi_weight': 1.5,
                'ofi_weight': 1.4,
                'cvd_weight': 1.3,
                'micro_price_weight': 1.2,
                'composite_threshold': 1.0,
                'slope_filter': {
                    'enabled': False,
                    'period': 10,
                    'threshold': 0.3
                }
            },
            'volatility': {
                'ewma_lambda': 0.2,
                'dynamic_obi': {
                    'enabled': True,
                    'volatility_factor': 3.0,
                    'min_threshold_factor': 0.7,
                    'max_threshold_factor': 2.0
                }
            },
            'twap': {
                'enabled': True,
                'max_order_size_btc': 0.05,
                'interval_seconds': 5,
                'partial_exit_enabled': False,
                'profit_threshold': 0.5,
                'exit_ratio': 0.5
            },
            'risk': {
                'max_drawdown_percent': 20,
                'max_position_ratio': 0.8
            }
        }

        nested_params = nest_params(flat_params)
        self.maxDiff = None # Show full diff on failure
        self.assertDictEqual(nested_params, expected_nested_params)

    def test_nest_params_partial(self):
        """
        Tests that a partial flat parameter dictionary is correctly converted,
        and that default nested structures are still created.
        """
        flat_params = {
            'spread_limit': 50,
            'long_tp': 150,
            'slope_filter_enabled': True,
            'slope_threshold': 0.5,
        }

        expected_nested_params = {
            'spread_limit': 50,
            'adaptive_position_sizing': {},
            'long': {
                'obi_threshold': 0.0,
                'tp': 150
            },
            'short': {
                'obi_threshold': 0.0
            },
            'signal': {
                'slope_filter': {
                    'enabled': True,
                    'threshold': 0.5
                }
            },
            'volatility': {
                'dynamic_obi': {}
            },
            'twap': {},
            'risk': {}
        }

        nested_params = nest_params(flat_params)
        self.maxDiff = None
        self.assertDictEqual(nested_params, expected_nested_params)

if __name__ == '__main__':
    unittest.main()
