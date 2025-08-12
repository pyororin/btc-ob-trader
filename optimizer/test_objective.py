import unittest
from unittest.mock import patch, MagicMock
import optuna

from optimizer.objective import Objective
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


        self.objective = Objective(self.mock_study)

    def tearDown(self):
        self.trial_user_attrs.clear()

    @patch('optimizer.objective.simulation.run_simulation')
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

    @patch('optimizer.objective.simulation.run_simulation')
    def test_pruning_on_zero_trades(self, mock_run_simulation):
        """Test that the trial is pruned if there are not enough trades."""
        mock_summary = {'TotalTrades': 0}
        mock_run_simulation.return_value = (mock_summary, "")
        config.MIN_TRADES_FOR_PRUNING = 5

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)
        self.assertEqual(self.trial_user_attrs.get("trades"), 0)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_pruning_on_failed_simulation(self, mock_run_simulation):
        """Test that the trial is pruned if the simulation fails."""
        mock_run_simulation.return_value = ({}, "error log")

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)

    @patch('optimizer.objective.simulation.run_simulation')
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

    @patch('optimizer.objective.simulation.run_simulation')
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

    @patch('optimizer.objective.simulation.run_simulation')
    def test_low_execution_rate_pruning(self, mock_run_simulation):
        """Test that a low execution rate correctly triggers pruning."""
        mock_summary = {'TotalTrades': 5, 'SharpeRatio': 2.0, 'PnlHistory': [10]*5}
        mock_log = "order NOT matched\n" * 10
        mock_run_simulation.return_value = (mock_summary, mock_log)

        with self.assertRaises(optuna.exceptions.TrialPruned):
            self.objective(self.mock_trial)
        self.assertLess(self.trial_user_attrs.get("execution_rate"), 0.5)

if __name__ == '__main__':
    unittest.main()
