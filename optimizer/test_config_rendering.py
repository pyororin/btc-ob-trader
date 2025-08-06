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

        # This list of side effects corresponds to the *active* list of parameters
        # in objective.py's _suggest_parameters method. It must be kept in sync.
        trial.suggest_int.side_effect = [
            80,    # spread_limit
            100,   # long_tp
            -100,  # long_sl
            100,   # short_tp
            -100,  # short_sl
        ]
        trial.suggest_float.side_effect = [
            0.4,   # micro_price_weight
            2.0,   # volatility_factor
        ]
        trial.suggest_categorical.side_effect = [
            1.2,   # obi_weight
            1.2,   # ofi_weight
            0.5,   # cvd_weight
            0.3,   # composite_threshold
            0.1,   # ewma_lambda
            True,  # dynamic_obi_enabled
            0.75,  # min_threshold_factor
            2.0,   # max_threshold_factor
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

        # 5. Assert that the parsed YAML matches the exact expected structure.
        # Values for optimized params come from the side_effect lists above.
        # Values for fixed params come from the .template file.
        expected_yaml_structure = {
            'pair': 'btc_jpy', 'order_amount': 0.01, 'spread_limit': 80,
            'lot_max_ratio': 0.95, 'order_ratio': 0.95,
            'adaptive_position_sizing': {
                'enabled': False, 'num_trades': 10, 'reduction_step': 0.8, 'min_ratio': 0.5
            },
            'long': {'tp': 100, 'sl': -100},
            'short': {'tp': 100, 'sl': -100},
            'signal': {
                'hold_duration_ms': 500, 'cvd_window_minutes': 1, 'obi_weight': 1.2,
                'ofi_weight': 1.2, 'cvd_weight': 0.5, 'micro_price_weight': 0.4,
                'composite_threshold': 0.3,
                'slope_filter': {'enabled': False, 'period': 44, 'threshold': 0.7}
            },
            'volatility': {
                'ewma_lambda': 0.1,
                'dynamic_obi': {
                    'enabled': True, 'volatility_factor': 2.0,
                    'min_threshold_factor': 0.75, 'max_threshold_factor': 2.0
                }
            },
            'twap': {
                'enabled': False, 'max_order_size_btc': 0.05, 'interval_seconds': 5,
                'partial_exit_enabled': False, 'profit_threshold': 1.0, 'exit_ratio': 0.5
            },
            'risk': {'max_drawdown_percent': 20, 'max_position_ratio': 0.9}
        }

        self.maxDiff = None
        self.assertDictEqual(parsed_yaml, expected_yaml_structure)

if __name__ == '__main__':
    unittest.main()
