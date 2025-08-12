import unittest
from unittest.mock import patch, MagicMock, call
import optuna

# Modules to test
from optimizer.study import create_study, warm_start_with_recent_trials
from optimizer.objective import Objective
from optimizer import config

class TestStudy(unittest.TestCase):
    """Tests for the study.py module."""

    @patch('optimizer.study.optuna.create_study')
    def test_create_study_single_objective(self, mock_create_study):
        """Verify that create_study configures a single-objective study correctly."""
        create_study("sqlite:///test.db", "test-study")

        self.assertTrue(mock_create_study.called)
        args, kwargs = mock_create_study.call_args
        self.assertIsInstance(kwargs['sampler'], optuna.samplers.TPESampler)
        self.assertEqual(kwargs['direction'], 'maximize')


if __name__ == '__main__':
    unittest.main()
