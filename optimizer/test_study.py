import unittest
from unittest.mock import patch, MagicMock, call
from pathlib import Path

# Assuming the optimizer package is in the python path
from optimizer import study

class TestStudyHelpers(unittest.TestCase):

    def test_unflatten_params(self):
        """
        Tests that the _unflatten_params function correctly converts
        a flat dictionary to a nested dictionary.
        """
        flat_params = {
            'spread_limit': 50,
            'adaptive_position_sizing.enabled': True,
            'adaptive_position_sizing.num_trades': 10,
            'signal.slope_filter.enabled': False,
            'signal.slope_filter.period': 5
        }
        expected_nested_params = {
            'spread_limit': 50,
            'adaptive_position_sizing': {
                'enabled': True,
                'num_trades': 10
            },
            'signal': {
                'slope_filter': {
                    'enabled': False,
                    'period': 5
                }
            }
        }
        self.assertEqual(study._unflatten_params(flat_params), expected_nested_params)

    def test_unflatten_params_empty(self):
        """Tests that the function handles an empty dictionary correctly."""
        self.assertEqual(study._unflatten_params({}), {})

    def test_unflatten_params_no_nesting(self):
        """Tests that the function handles a dictionary with no dots."""
        params = {'a': 1, 'b': 'hello', 'c': False}
        self.assertEqual(study._unflatten_params(params), params)

    @patch('optimizer.study._save_best_parameters')
    @patch('optimizer.study.run_simulation')
    def test_perform_oos_validation_uses_unflattened_params(
        self, mock_run_simulation, mock_save_best_parameters
    ):
        """
        Tests that _perform_oos_validation unnests params before calling
        run_simulation and _save_best_parameters.
        """
        # 1. Setup
        flat_params = {'a.b': 1, 'c': True}
        nested_params = {'a': {'b': 1}, 'c': True}

        # Mock candidate list as created by _get_oos_candidates
        candidates = [{'params': flat_params, 'source': 'is_rank_1'}]

        # Mock simulation result to pass validation
        mock_run_simulation.return_value = {
            'TotalTrades': 100,
            'SharpeRatio': 2.0,
            'ProfitFactor': 1.5
        }

        # 2. Execute
        # We need a dummy path that exists
        dummy_path = Path('.')
        result = study._perform_oos_validation(candidates, dummy_path)

        # 3. Assert
        self.assertTrue(result) # Should pass validation and return True

        # Check that run_simulation was called with the NESTED params
        mock_run_simulation.assert_called_once_with(nested_params, dummy_path)

        # Check that _save_best_parameters was also called with the NESTED params
        mock_save_best_parameters.assert_called_once_with(nested_params)

if __name__ == '__main__':
    unittest.main()
