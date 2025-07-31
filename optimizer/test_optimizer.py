import unittest
from unittest.mock import patch, MagicMock
import optuna

# Modules to test
from .study import create_study
from .objective import Objective
from . import config

class TestStudy(unittest.TestCase):
    """Tests for the study.py module."""

    @patch('optimizer.study.optuna.create_study')
    def test_create_study_multi_objective(self, mock_create_study):
        """Verify that create_study configures a multi-objective study correctly."""
        create_study()

        # Check that optuna.create_study was called
        self.assertTrue(mock_create_study.called)

        # Get the arguments passed to optuna.create_study
        args, kwargs = mock_create_study.call_args

        # Check the sampler
        self.assertIsInstance(kwargs['sampler'], optuna.samplers.MOTPESampler)

        # Check the directions
        expected_directions = ['maximize', 'maximize', 'minimize']
        self.assertEqual(kwargs['directions'], expected_directions)

class TestObjective(unittest.TestCase):
    """Tests for the objective.py module."""

    def setUp(self):
        """Set up a mock study and objective instance for testing."""
        self.study = optuna.create_study(directions=['maximize', 'maximize', 'minimize'])
        # The objective function requires this user attribute to be set.
        self.study.set_user_attr('current_csv_path', 'dummy/path.csv')
        self.objective = Objective(self.study)

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_success(self, mock_run_simulation):
        """Test a successful trial execution."""
        # Mock the simulation result
        mock_summary = {
            'TotalTrades': 100,
            'SharpeRatio': 1.5,
            'WinRate': 60.0,
            'MaxDrawdown': 1234.5,
            'PnlHistory': [10, -5, 10, -5, 10] # Results in a low relative drawdown
        }
        mock_run_simulation.return_value = mock_summary

        trial = self.study.ask()
        # Set some dummy params
        trial.suggest_int('spread_limit', 20, 80)

        result = self.objective(trial)

        self.assertEqual(result, (1.5, 60.0, 1234.5))

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_high_drawdown_penalty(self, mock_run_simulation):
        """Test that a high relative drawdown triggers the penalty."""
        # Mock a result that will cause high relative drawdown (final PnL is low)
        mock_summary = {
            'TotalTrades': 100,
            'SharpeRatio': 0.1,
            'WinRate': 51.0,
            'MaxDrawdown': 500.0,
            'PnlHistory': [100, -20, -80, 10] # Peak is 100, final PnL is 10, drawdown is 90. Relative DD = 90 / 10 = 9.0 > 0.25
        }
        mock_run_simulation.return_value = mock_summary

        trial = self.study.ask()
        trial.suggest_int('spread_limit', 20, 80)

        result = self.objective(trial)

        # Check if the penalty values are returned
        self.assertEqual(result, (-100.0, 0.0, 1_000_000.0))

    @patch('optimizer.objective.simulation.run_simulation')
    def test_objective_penalized_low_trades(self, mock_run_simulation):
        """Test that a trial is penalized if it has too few trades."""
        # Mock a result with very few trades
        mock_summary = {
            'TotalTrades': config.MIN_TRADES_FOR_PRUNING - 1, # One less than required
            'SharpeRatio': 2.0,
            'WinRate': 70.0,
            'MaxDrawdown': 100.0,
            'PnlHistory': [10, 10]
        }
        mock_run_simulation.return_value = mock_summary

        trial = self.study.ask()
        trial.suggest_int('spread_limit', 20, 80)

        result = self.objective(trial)
        self.assertEqual(result, (-100.0, 0.0, 1_000_000.0))

    def test_objective_no_sim_path(self):
        """Test that a penalty is returned if the simulation path is missing."""
        # Create a study without the user attribute
        study_no_path = optuna.create_study(directions=['maximize', 'maximize', 'minimize'])
        objective_no_path = Objective(study_no_path)
        trial = study_no_path.ask()
        trial.suggest_int('spread_limit', 20, 80)

        result = objective_no_path(trial)
        self.assertEqual(result, (-100.0, 0.0, 1_000_000.0))

if __name__ == '__main__':
    unittest.main()
