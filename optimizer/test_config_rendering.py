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
        # 1. Mock the Optuna Trial and Study objects
        trial = MagicMock(spec=optuna.Trial)
        mock_study = MagicMock(spec=optuna.Study)
        mock_study.user_attrs = {} # Objective expects user_attrs to exist

        # Mock the suggestion methods based on the new config
        # The order must match the order in config/optimizer_config.yaml
        trial.suggest_int.side_effect = [
            100,   # long_tp
            -100,  # long_sl
            110,   # short_tp
            -110,  # short_sl
            100,   # entry_price_offset
        ]
        trial.suggest_float.side_effect = [
            1.5,   # obi_weight
            1.4,   # ofi_weight
            1.3,   # cvd_weight
            0.4,   # micro_price_weight
            0.25,  # composite_threshold
            0.2,   # ewma_lambda
            # Conditional params below
            3.0,   # volatility_factor
            0.7,   # min_threshold_factor
            2.5,   # max_threshold_factor
        ]
        # The only categorical parameter left
        trial.suggest_categorical.return_value = True # dynamic_obi_enabled

        # 2. Instantiate Objective and suggest parameters
        mock_sim_manager = MagicMock(spec=Objective.__init__.__annotations__['sim_manager'])
        objective = Objective(study=mock_study, sim_manager=mock_sim_manager)
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
            'pair': 'btc_jpy', 'order_amount': 0.01,
            'entry_price_offset': 100,
            'lot_max_ratio': 1.0, 'order_ratio': 0.95,
            'long': {'tp': 100, 'sl': -100},
            'short': {'tp': 110, 'sl': -110},
            'signal': {
                'hold_duration_ms': 500, 'cvd_window_minutes': 1, 'obi_weight': 1.5,
                'ofi_weight': 1.4, 'cvd_weight': 1.3, 'micro_price_weight': 0.4,
                'composite_threshold': 0.25
            },
            'volatility': {
                'ewma_lambda': 0.2,
                'dynamic_obi': {
                    'enabled': True, 'volatility_factor': 3.0,
                    'min_threshold_factor': 0.7, 'max_threshold_factor': 2.5
                }
            },
            'risk': {'max_drawdown_percent': 25, 'max_position_ratio': 1.0}
        }

        self.maxDiff = None
        self.assertDictEqual(parsed_yaml, expected_yaml_structure)

if __name__ == '__main__':
    unittest.main()
