import unittest
from unittest.mock import MagicMock
import yaml
import optuna

from optimizer.objective import Objective
from optimizer.config import CONFIG_TEMPLATE_PATH, PARAMS_DIR
from optimizer.utils import finalize_for_yaml, nest_params
from jinja2 import Environment, FileSystemLoader

class TestConfigRendering(unittest.TestCase):

    def test_render_config_correctly_and_fully(self):
        """
        Tests if a full set of parameters suggested by the Objective class
        are correctly rendered into a valid, complete YAML config file.
        """
        # 1. Mock the Optuna Trial object to return a full set of values
        trial = MagicMock(spec=optuna.Trial)

        # This list of side effects corresponds to the full list of parameters
        # in objective.py, in the order they appear.
        trial.suggest_int.side_effect = [
            80,    # spread_limit
            10,    # adaptive_num_trades
            100,   # long_tp
            -100,  # long_sl
            110,   # short_tp
            -110,  # short_sl
            500,   # hold_duration_ms
            10,    # slope_period
            5,     # twap_interval_seconds
            20     # risk_max_drawdown_percent
        ]
        trial.suggest_float.side_effect = [
            0.9,   # lot_max_ratio
            0.9,   # order_ratio
            0.8,   # adaptive_reduction_step
            0.4,   # adaptive_min_ratio
            0.5,   # long_obi_threshold
            0.6,   # short_obi_threshold
            1.5,   # obi_weight
            1.4,   # ofi_weight
            1.3,   # cvd_weight
            1.2,   # micro_price_weight
            1.0,   # composite_threshold
            0.3,   # slope_threshold
            0.2,   # ewma_lambda
            3.0,   # volatility_factor
            0.7,   # min_threshold_factor
            2.0,   # max_threshold_factor
            0.05,  # twap_max_order_size_btc
            0.5,   # twap_profit_threshold
            0.4,   # twap_exit_ratio
            0.8    # risk_max_position_ratio
        ]
        trial.suggest_categorical.side_effect = [
            True,  # adaptive_position_sizing_enabled
            False, # slope_filter_enabled
            True,  # dynamic_obi_enabled
            True,  # twap_enabled
            False  # twap_partial_exit_enabled
        ]

        # 2. Instantiate Objective and suggest parameters
        objective = Objective(study=None)
        flat_params = objective._suggest_parameters(trial)
        params = nest_params(flat_params) # Convert to nested structure

        # 3. Load and render the template using the correct environment
        env = Environment(
            loader=FileSystemLoader(searchpath=PARAMS_DIR),
            finalize=finalize_for_yaml
        )
        template = env.get_template(CONFIG_TEMPLATE_PATH.name)
        rendered_yaml_str = template.render(params)

        # 4. Parse the rendered YAML to validate its structure
        try:
            parsed_yaml = yaml.safe_load(rendered_yaml_str)
        except yaml.YAMLError as e:
            self.fail(f"Rendered YAML is not valid: {e}\nContent:\n{rendered_yaml_str}")

        # 5. Assert that the parsed YAML matches the exact expected structure
        expected_yaml_structure = {
            'pair': 'btc_jpy', 'order_amount': 0.01, 'spread_limit': 80,
            'lot_max_ratio': 0.9, 'order_ratio': 0.9,
            'adaptive_position_sizing': {
                'enabled': True, 'num_trades': 10, 'reduction_step': 0.8, 'min_ratio': 0.4
            },
            'long': {'obi_threshold': 0.5, 'tp': 100, 'sl': -100},
            'short': {'obi_threshold': 0.6, 'tp': 110, 'sl': -110},
            'signal': {
                'hold_duration_ms': 500, 'cvd_window_minutes': 1, 'obi_weight': 1.5,
                'ofi_weight': 1.4, 'cvd_weight': 1.3, 'micro_price_weight': 1.2,
                'composite_threshold': 1.0,
                'slope_filter': {'enabled': False, 'period': 10, 'threshold': 0.3}
            },
            'volatility': {
                'ewma_lambda': 0.2,
                'dynamic_obi': {
                    'enabled': True, 'volatility_factor': 3.0,
                    'min_threshold_factor': 0.7, 'max_threshold_factor': 2.0
                }
            },
            'twap': {
                'enabled': True, 'max_order_size_btc': 0.05, 'interval_seconds': 5,
                'partial_exit_enabled': False, 'profit_threshold': 0.5, 'exit_ratio': 0.4
            },
            'risk': {'max_drawdown_percent': 20, 'max_position_ratio': 0.8}
        }

        self.maxDiff = None
        self.assertDictEqual(parsed_yaml, expected_yaml_structure)

if __name__ == '__main__':
    unittest.main()
