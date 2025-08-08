import unittest
from unittest.mock import patch, MagicMock
import optuna

from optimizer.objective import Objective
from optimizer import config

class TestObjective(unittest.TestCase):

    def setUp(self):
        """Set up a mock study and trial for each test."""
        # This dictionary will act as a backing store for the mock's user_attrs
        self.trial_user_attrs = {}

        self.mock_study = MagicMock(spec=optuna.Study)
        self.mock_study.user_attrs = {'current_csv_path': 'dummy.csv'}

        self.mock_trial = MagicMock(spec=optuna.trial.Trial)

        # Configure the mock's side effects to use our real dictionary,
        # simulating the behavior of Optuna's trial user_attrs.
        def set_attr_side_effect(key, value):
            self.trial_user_attrs[key] = value

        def get_attr_side_effect(key, default=None):
            return self.trial_user_attrs.get(key, default)

        self.mock_trial.set_user_attr.side_effect = set_attr_side_effect
        self.mock_trial.user_attrs.get.side_effect = get_attr_side_effect

        # Mock the suggest_... methods to return fixed values
        self.mock_trial.suggest_int.return_value = 1
        self.mock_trial.suggest_float.return_value = 0.5
        self.mock_trial.suggest_categorical.return_value = True

        self.objective = Objective(self.mock_study)

    def tearDown(self):
        """Clear the user_attrs dict after each test to ensure isolation."""
        self.trial_user_attrs.clear()

    def get_penalty_value(self):
        """Helper to get the consistent penalty value."""
        return -1.0, 0.0, 1_000_000.0

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_successful_simulation(self, mock_run_simulation):
        """
        Test the objective function when simulation returns successful results with trades.
        """
        # Arrange
        mock_summary = {
            'TotalTrades': 50, 'SharpeRatio': 2.1, 'ProfitFactor': 1.8, 'WinRate': 65.0,
            'MaxDrawdown': 500.0, 'PnlHistory': [100, -50, 150]
        }
        mock_log = "Confirmed LONG signal\n" * 50 # 50 signals, 50 trades -> 1.0 realization
        mock_run_simulation.return_value = (mock_summary, mock_log)
        config.MIN_TRADES_FOR_PRUNING = 10

        # Act
        sqn, pf, mdd = self.objective(self.mock_trial)

        # Assert
        realization_rate = self.trial_user_attrs.get("realization_rate")
        execution_rate = self.trial_user_attrs.get("execution_rate")
        expected_sqn = 2.1 * (50 ** 0.5) * realization_rate * execution_rate

        self.assertAlmostEqual(sqn, expected_sqn)
        self.assertEqual(pf, 1.8)
        self.assertEqual(mdd, -500.0)
        self.assertEqual(self.trial_user_attrs.get("trades"), 50)
        self.assertEqual(self.trial_user_attrs.get("realization_rate"), 1.0)
        self.assertEqual(self.trial_user_attrs.get("execution_rate"), 1.0)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_zero_trades(self, mock_run_simulation):
        """
        Test the objective function when simulation returns zero trades.
        """
        # Arrange
        mock_summary = {'TotalTrades': 0, 'SharpeRatio': 0.0, 'WinRate': 0.0, 'MaxDrawdown': 0.0, 'PnlHistory': []}
        mock_run_simulation.return_value = (mock_summary, "")

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        self.assertEqual(result, (-1.0, 0.0, -1_000_000.0))
        self.assertEqual(self.trial_user_attrs.get("trades"), 0)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_failed_simulation(self, mock_run_simulation):
        """
        Test the objective function when simulation fails.
        """
        # Arrange
        mock_run_simulation.return_value = ({}, "error log")

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        self.assertEqual(result, (-1.0, 0.0, -1_000_000.0))

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_high_drawdown_penalty(self, mock_run_simulation):
        """
        Test that a high relative drawdown correctly triggers a penalty.
        """
        # Arrange
        mock_summary = {
            'TotalTrades': 50, 'SharpeRatio': 2.0, 'WinRate': 70.0, 'MaxDrawdown': 1000.0,
            'PnlHistory': [500, 500, -800, 300] # Final PnL=500, Max DD=800 -> Rel DD=1.6
        }
        mock_run_simulation.return_value = (mock_summary, "")

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        self.assertEqual(result, (-1.0, 0.0, -1_000_000.0))
        self.assertGreater(self.trial_user_attrs.get("relative_drawdown"), 0.25)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_execution_rate_logic(self, mock_run_simulation):
        """
        Test if execution_rate and related metrics are calculated correctly.
        """
        # Arrange
        mock_summary = {'TotalTrades': 10, 'SharpeRatio': 1.5, 'ProfitFactor': 2.0, 'MaxDrawdown': 200, 'PnlHistory': [100]*10}
        mock_log = "Confirmed LONG signal\n" * 20 + "order NOT matched\n" * 5 # 20 signals, 10 trades, 5 not matched
        mock_run_simulation.return_value = (mock_summary, mock_log)
        config.MIN_TRADES_FOR_PRUNING = 5

        # Act
        self.objective(self.mock_trial)

        # Assert
        self.assertEqual(self.trial_user_attrs.get("confirmed_signals"), 20)
        self.assertEqual(self.trial_user_attrs.get("unrealized_trades"), 5)
        self.assertAlmostEqual(self.trial_user_attrs.get("realization_rate"), 10 / 20)
        self.assertAlmostEqual(self.trial_user_attrs.get("execution_rate"), 10 / (10 + 5))

    @patch('optimizer.objective.simulation.run_simulation')
    def test_low_execution_rate_pruning(self, mock_run_simulation):
        """
        Test that a low execution rate correctly triggers pruning.
        """
        # Arrange
        mock_summary = {'TotalTrades': 5, 'SharpeRatio': 2.0, 'ProfitFactor': 3.0, 'MaxDrawdown': 100, 'PnlHistory': [100]*5}
        mock_log = "order NOT matched\n" * 10 # 5 trades, 10 not matched -> execution_rate = 5/15 = 0.33
        mock_run_simulation.return_value = (mock_summary, mock_log)

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        self.assertEqual(result, (-1.0, 0.0, -1_000_000.0))
        self.assertLess(self.trial_user_attrs.get("execution_rate"), 0.5)

if __name__ == '__main__':
    unittest.main()
