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
        # Because the jittered runs will all return the same mock value, the stdev will be 0.
        # The final score will be the mean score, which is just the value from this summary.
        mock_summary = {
            'TotalTrades': 50,
            'SharpeRatio': 2.1,
            'ProfitFactor': 1.8,
            'WinRate': 65.0,
            'MaxDrawdown': 500.0,
            'PnlHistory': [100, -50, 150] # Relative drawdown is low, no penalty
        }
        mock_run_simulation.return_value = mock_summary
        config.MIN_TRADES_FOR_PRUNING = 10 # Ensure pruning is not triggered

        # Act
        sqn, pf, mdd = self.objective(self.mock_trial)

        # Assert
        expected_sqn = 2.1 * (50 ** 0.5)
        self.assertAlmostEqual(sqn, expected_sqn)
        self.assertEqual(pf, 1.8)
        self.assertEqual(mdd, -500.0)

        # Check that the backing dictionary was populated correctly for later analysis
        self.assertEqual(self.trial_user_attrs.get("trades"), 50)
        self.assertEqual(self.trial_user_attrs.get("sharpe_ratio"), 2.1)
        self.assertEqual(self.trial_user_attrs.get("profit_factor"), 1.8)
        self.assertAlmostEqual(self.trial_user_attrs.get("sqn"), expected_sqn)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_zero_trades(self, mock_run_simulation):
        """
        Test the objective function when simulation returns zero trades.
        It should return a penalty value.
        """
        # Arrange
        mock_summary = {
            'TotalTrades': 0, 'SharpeRatio': 0.0, 'WinRate': 0.0, 'MaxDrawdown': 0.0,
            'PnlHistory': []
        }
        mock_run_simulation.return_value = mock_summary

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        # The penalty now returns a large negative number for the 3rd objective
        # because the direction is 'maximize'.
        self.assertEqual(result[0], -1.0)
        self.assertEqual(result[1], 0.0)
        self.assertEqual(result[2], -1_000_000.0)
        # Check that attributes were still set before penalty was returned
        self.assertEqual(self.trial_user_attrs.get("trades"), 0)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_failed_simulation(self, mock_run_simulation):
        """
        Test the objective function when simulation fails (returns empty dict).
        It should return a penalty value.
        """
        # Arrange
        mock_run_simulation.return_value = {}

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        self.assertEqual(result[0], -1.0)
        self.assertEqual(result[1], 0.0)
        self.assertEqual(result[2], -1_000_000.0)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_with_high_drawdown_penalty(self, mock_run_simulation):
        """
        Test that a high relative drawdown correctly triggers a penalty.
        """
        # Arrange
        mock_summary = {
            'TotalTrades': 50, 'SharpeRatio': 2.0, 'WinRate': 70.0, 'MaxDrawdown': 1000.0,
            'PnlHistory': [500, 500, -300, 300] # Final PnL=1000, Max DD=300 -> Rel DD=0.3
        }
        mock_run_simulation.return_value = mock_summary

        # Act
        result = self.objective(self.mock_trial)

        # Assert
        self.assertEqual(result[0], -1.0)
        self.assertEqual(result[1], 0.0)
        self.assertEqual(result[2], -1_000_000.0)
        # Check that relative_drawdown was calculated and was the cause of the penalty
        self.assertGreater(self.trial_user_attrs.get("relative_drawdown"), 0.25)

if __name__ == '__main__':
    unittest.main()
