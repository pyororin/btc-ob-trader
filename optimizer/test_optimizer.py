import unittest
from unittest.mock import patch, MagicMock, call
import optuna

# Modules to test
from .study import create_study, warm_start_with_recent_trials
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

        # Check the sampler (updated to NSGAIISampler)
        self.assertIsInstance(kwargs['sampler'], optuna.samplers.NSGAIISampler)

        # Check the directions
        expected_directions = ['maximize', 'maximize', 'minimize']
        self.assertEqual(kwargs['directions'], expected_directions)

    @patch('optimizer.study.optuna.load_study')
    @patch('optimizer.study.optuna.get_all_study_summaries')
    def test_warm_start_with_mixed_timezones(self, mock_get_summaries, mock_load_study):
        """Test warm start handles trials with and without timezone information."""
        from datetime import datetime, timezone, timedelta

        # 1. Setup mock data
        now = datetime.now(timezone.utc)

        # Create mock trials with the necessary attributes for the logic to work
        aware_trial = optuna.create_trial(
            state=optuna.trial.TrialState.COMPLETE,
            values=[1.0, 0.5, 100.0],
            params={'param1': 1},
            distributions={'param1': optuna.distributions.IntDistribution(1, 1)}
        )
        aware_trial.datetime_complete = now - timedelta(days=1)

        naive_trial = optuna.create_trial(
            state=optuna.trial.TrialState.COMPLETE,
            values=[1.1, 0.6, 110.0],
            params={'param1': 2},
            distributions={'param1': optuna.distributions.IntDistribution(1, 2)}
        )
        naive_trial.datetime_complete = now.replace(tzinfo=None) - timedelta(days=2)

        old_trial = optuna.create_trial(
            state=optuna.trial.TrialState.COMPLETE,
            values=[0.9, 0.4, 90.0],
            params={'param1': 3},
            distributions={'param1': optuna.distributions.IntDistribution(1, 3)}
        )
        old_trial.datetime_complete = now - timedelta(days=30)

        incomplete_trial = optuna.create_trial(state=optuna.trial.TrialState.RUNNING)
        incomplete_trial.datetime_complete = None

        mock_study_summary = MagicMock()
        mock_study_summary.study_name = "previous-study-123"
        mock_get_summaries.return_value = [mock_study_summary]

        mock_previous_study = MagicMock()
        mock_previous_study.trials = [aware_trial, naive_trial, old_trial, incomplete_trial]
        mock_load_study.return_value = mock_previous_study

        # 2. Setup the current study and mock its add_trial method
        current_study = optuna.create_study(directions=['maximize', 'maximize', 'minimize'])
        current_study.add_trial = MagicMock()

        # 3. Run the function to be tested
        warm_start_with_recent_trials(current_study, recent_days=10)

        # 4. Assertions
        # Should have been called twice (for the two recent, complete trials)
        self.assertEqual(current_study.add_trial.call_count, 2)

        # Check that it was called with the correct trials
        calls = current_study.add_trial.call_args_list
        self.assertIn(call(aware_trial), calls)
        self.assertIn(call(naive_trial), calls)

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
            'ProfitFactor': 1.6,
            'WinRate': 60.0,
            'MaxDrawdown': 1234.5,
            'PnlHistory': [10, -5, 10, -5, 10] # Results in a low relative drawdown
        }
        mock_run_simulation.return_value = mock_summary

        trial = self.study.ask()
        # Set some dummy params
        trial.suggest_int('spread_limit', 20, 80)

        result = self.objective(trial)

        expected_sqn = 1.5 * (100 ** 0.5)
        expected_pf = 1.6
        expected_mdd = 1234.5

        self.assertAlmostEqual(result[0], expected_sqn)
        self.assertAlmostEqual(result[1], expected_pf)
        self.assertEqual(result[2], expected_mdd)

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

        # Check if the dominated penalty values are returned
        self.assertEqual(result[0], -1.0)
        self.assertEqual(result[1], 0.0)
        self.assertEqual(result[2], 1_000_000.0)

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
        self.assertEqual(result[0], -1.0)
        self.assertEqual(result[1], 0.0)
        self.assertEqual(result[2], 1_000_000.0)

    def test_objective_no_sim_path(self):
        """Test that a penalty is returned if the simulation path is missing."""
        # Create a study without the user attribute
        study_no_path = optuna.create_study(directions=['maximize', 'maximize', 'minimize'])
        objective_no_path = Objective(study_no_path)
        trial = study_no_path.ask()
        trial.suggest_int('spread_limit', 20, 80)

        result = objective_no_path(trial)
        self.assertEqual(result[0], -1.0)
        self.assertEqual(result[1], 0.0)
        self.assertEqual(result[2], 1_000_000.0)

if __name__ == '__main__':
    unittest.main()
