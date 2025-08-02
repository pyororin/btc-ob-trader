import unittest
from unittest.mock import MagicMock
import yaml
from jinja2 import Template
import optuna

from optimizer.objective import Objective
from optimizer.config import CONFIG_TEMPLATE_PATH

class TestConfigRendering(unittest.TestCase):

    def test_render_config_correctly(self):
        """
        Tests if the parameters suggested by the Objective class are correctly
        rendered into a valid YAML config file that matches the template's structure.
        """
        # 1. Mock the Optuna Trial object
        trial = MagicMock(spec=optuna.Trial)

        # Define the return values for the mock trial's suggest methods
        # This simulates a specific set of choices made during an Optuna trial.
        trial.suggest_int.side_effect = [
            80,    # spread_limit
            10,    # adaptive_num_trades
            100,   # long_tp
            -100,  # long_sl
            100,   # short_tp
            -100,  # short_sl
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
            1.5,   # obi_weight
            1.5,   # ofi_weight
            1.5,   # cvd_weight
            1.5,   # micro_price_weight
            1.0,   # composite_threshold
            0.3,   # slope_threshold
            0.2,   # ewma_lambda
            3.0,   # volatility_factor
            0.7,   # min_threshold_factor
            2.0,   # max_threshold_factor
            0.05,  # twap_max_order_size_btc
            0.5,   # twap_profit_threshold
            0.5,   # twap_exit_ratio
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
        # We don't need a real study object for this test.
        objective = Objective(study=None)
        params = objective._suggest_parameters(trial)

        # 3. Load the Jinja2 template using the correct environment
        # This mirrors the implementation in simulation.py to ensure the test is accurate
        from optimizer.utils import finalize_for_yaml
        from jinja2 import Environment, FileSystemLoader
        from optimizer.config import PARAMS_DIR

        env = Environment(
            loader=FileSystemLoader(searchpath=PARAMS_DIR),
            finalize=finalize_for_yaml
        )
        template = env.get_template(CONFIG_TEMPLATE_PATH.name)

        # 4. Render the template
        rendered_yaml_str = template.render(params)

        # Optional: Print the rendered YAML for debugging
        print("--- Rendered YAML ---")
        print(rendered_yaml_str)
        print("---------------------")

        # 5. Parse the rendered YAML to validate its structure
        try:
            parsed_yaml = yaml.safe_load(rendered_yaml_str)
        except yaml.YAMLError as e:
            self.fail(f"Rendered YAML is not valid: {e}\nContent:\n{rendered_yaml_str}")

        # 6. Assert that key values and types are correct
        self.assertIsInstance(parsed_yaml, dict)

        # Check a few key parameters of different types and nesting levels
        self.assertEqual(parsed_yaml['spread_limit'], 80)
        self.assertEqual(parsed_yaml['lot_max_ratio'], 0.9)

        # Check nested dictionary for adaptive_position_sizing
        self.assertTrue(parsed_yaml['adaptive_position_sizing']['enabled'])
        self.assertEqual(parsed_yaml['adaptive_position_sizing']['num_trades'], 10)

        # Check boolean conversion (should be true/false, not True/False)
        self.assertIn("enabled: true", rendered_yaml_str)
        self.assertIn("enabled: false", rendered_yaml_str)

        # Check another nested dictionary
        self.assertEqual(parsed_yaml['long']['tp'], 100)

        # Check signal weights
        self.assertEqual(parsed_yaml['signal']['obi_weight'], 1.5)
        self.assertEqual(parsed_yaml['signal']['slope_filter']['enabled'], False)

        # Check hardcoded value is present
        self.assertEqual(parsed_yaml['signal']['cvd_window_minutes'], 1)

if __name__ == '__main__':
    unittest.main()
