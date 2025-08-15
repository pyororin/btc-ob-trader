import unittest
from unittest.mock import patch, MagicMock
import optuna

from unittest.mock import patch
from optimizer.objective import Objective, SimulationManager
from optimizer import config

class TestObjective(unittest.TestCase):

    def setUp(self):
        """Set up a mock study and trial for each test."""
        self.trial_user_attrs = {}
        self.mock_study = MagicMock(spec=optuna.Study)
        self.mock_study.user_attrs = {'current_csv_path': 'dummy.csv'}
        self.mock_trial = MagicMock(spec=optuna.trial.Trial)
        self.mock_trial.number = 1

        def set_attr_side_effect(key, value):
            self.trial_user_attrs[key] = value
        def get_attr_side_effect(key, default=None):
            return self.trial_user_attrs.get(key, default)

        self.mock_trial.set_user_attr.side_effect = set_attr_side_effect
        self.mock_trial.user_attrs.get.side_effect = get_attr_side_effect
        self.mock_trial.suggest_int.return_value = 1
        self.mock_trial.suggest_float.return_value = 0.5
        self.mock_trial.suggest_categorical.return_value = True
        # Mock distributions for jitter calculation
        self.mock_trial.params = {'param1': 0.5}
        self.mock_trial.distributions = {'param1': optuna.distributions.FloatDistribution(0, 1)}

        self.mock_sim_manager = MagicMock(spec=SimulationManager)
        self.objective = Objective(self.mock_study, self.mock_sim_manager)

    def tearDown(self):
        self.trial_user_attrs.clear()

    @patch('optimizer.simulation.SimulationRunner.run')
    def test_objective_with_successful_simulation(self, mock_run_simulation):
        """Test the objective function returns a penalized Sharpe Ratio."""
        mock_summary = {
            'TotalTrades': 50, 'SharpeRatio': 2.1, 'ProfitFactor': 1.8,
            'MaxDrawdown': 500.0, 'PnlHistory': [100, -50, 150]
        }
        mock_log = "Confirmed LONG signal\n" * 50
        mock_run_simulation.return_value = (mock_summary, mock_log)
        config.MIN_TRADES_FOR_PRUNING = 10
        config.DD_PENALTY_THRESHOLD = 0.5
        config.STABILITY_CHECK_N_RUNS = 1 # Simplify stability check for this test
        config.STABILITY_PENALTY_FACTOR = 0.1

        result = self.objective(self.mock_trial)

        self.assertIsInstance(result, float)
        # With 1 stability run, stdev is 0, so penalty is 0
        expected_sr = 2.1 * 1.0 * 1.0 # SR * realization_rate * execution_rate
        self.assertAlmostEqual(result, expected_sr)
        self.assertEqual(self.trial_user_attrs.get("trades"), 50)
        self.assertAlmostEqual(self.trial_user_attrs.get("final_sharpe_ratio_penalized"), expected_sr)

    @patch('optimizer.simulation.SimulationRunner.run')
    def test_pruning_on_zero_trades(self, mock_run_simulation):
        """Test that the trial is pruned if there are not enough trades."""
        mock_summary = {'TotalTrades': 0}
        mock_run_simulation.return_value = (mock_summary, "")
        config.MIN_TRADES_FOR_PRUNING = 5

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)
        self.assertEqual(self.trial_user_attrs.get("trades"), 0)

    @patch('optimizer.simulation.SimulationRunner.run')
    def test_pruning_on_failed_simulation(self, mock_run_simulation):
        """Test that the trial is pruned if the simulation fails."""
        mock_run_simulation.return_value = ({}, "error log")

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)

    @patch('optimizer.simulation.SimulationRunner.run')
    def test_pruning_on_high_drawdown(self, mock_run_simulation):
        """Test that a high relative drawdown correctly triggers pruning."""
        mock_summary = {
            'TotalTrades': 50, 'SharpeRatio': 2.0, 'PnlHistory': [500, 500, -800, 300]
        }
        mock_run_simulation.return_value = (mock_summary, "")
        config.DD_PENALTY_THRESHOLD = 0.25

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)
        self.assertGreater(self.trial_user_attrs.get("relative_drawdown"), 0.25)

    @patch('optimizer.simulation.SimulationRunner.run')
    def test_execution_rate_logic(self, mock_run_simulation):
        """Test if execution_rate and related metrics are calculated correctly."""
        mock_summary = {'TotalTrades': 10, 'SharpeRatio': 1.5, 'PnlHistory': [10]*10}
        mock_log = "Confirmed LONG signal\n" * 20 + "order NOT matched\n" * 5
        mock_run_simulation.return_value = (mock_summary, mock_log)
        config.MIN_TRADES_FOR_PRUNING = 5
        config.DD_PENALTY_THRESHOLD = 0.5

        self.objective(self.mock_trial)

        self.assertEqual(self.trial_user_attrs.get("confirmed_signals"), 20)
        self.assertEqual(self.trial_user_attrs.get("unrealized_trades"), 5)
        self.assertAlmostEqual(self.trial_user_attrs.get("realization_rate"), 10 / 20)
        self.assertAlmostEqual(self.trial_user_attrs.get("execution_rate"), 10 / (10 + 5))

    @patch('optimizer.simulation.SimulationRunner.run')
    def test_low_execution_rate_pruning(self, mock_run_simulation):
        """Test that a low execution rate correctly triggers pruning."""
        mock_summary = {'TotalTrades': 5, 'SharpeRatio': 2.0, 'PnlHistory': [10]*5}
        mock_log = "order NOT matched\n" * 10
        mock_run_simulation.return_value = (mock_summary, mock_log)

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)
        self.assertLess(self.trial_user_attrs.get("execution_rate"), 0.5)

    @patch('optimizer.config.PARAMETER_SPACE', {
        'param_int': {'type': 'int', 'low': 1, 'high': 10},
        'param_float': {'type': 'float', 'low': 0.1, 'high': 1.0, 'log': True},
        'param_cat': {'type': 'categorical', 'choices': ['a', 'b']},
        'dynamic_obi_enabled': {'type': 'categorical', 'choices': [True, False]},
        'volatility_factor': {'type': 'float', 'low': 0.5, 'high': 1.5}
    })
    def test_suggest_parameters_from_config(self):
        """
        Test that parameters are suggested based on the PARAMETER_SPACE config.
        """
        # We need to reset the mock to clear previous return values for this specific test
        self.mock_trial.suggest_categorical.side_effect = [True, 'a'] # First call for dynamic_obi_enabled, second for param_cat if needed

        params = self.objective._suggest_parameters(self.mock_trial)

        # Verify that the suggest methods were called correctly
        self.mock_trial.suggest_int.assert_called_once_with('param_int', 1, 10)
        self.mock_trial.suggest_float.assert_any_call('param_float', 0.1, 1.0, log=True)

        # Check the call for the categorical parameter
        self.mock_trial.suggest_categorical.assert_any_call('param_cat', ['a', 'b'])

        # Check the call for the conditional parameter
        # The first call to suggest_categorical will return True, enabling the condition
        self.mock_trial.suggest_float.assert_any_call('volatility_factor', 0.5, 1.5, log=False)

        # Verify the returned params dict
        self.assertIn('param_int', params)
        self.assertIn('param_float', params)
        self.assertIn('param_cat', params)
        self.assertIn('volatility_factor', params)


if __name__ == '__main__':
    unittest.main()
