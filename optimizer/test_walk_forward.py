import unittest
from unittest.mock import patch, MagicMock
import datetime
from pathlib import Path
import optuna

from optimizer import walk_forward
from optimizer import config

class TestWalkForwardAnalysis(unittest.TestCase):

    @patch('optimizer.walk_forward.datetime')
    @patch('optimizer.walk_forward.config')
    def test_generate_wfa_folds_success(self, mock_config, mock_datetime_module):
        """
        Tests if _generate_wfa_folds creates the correct date ranges for folds.
        """
        # --- Setup ---
        # We need to mock the datetime module, but ensure that timedelta still works.
        mock_datetime_module.timedelta = datetime.timedelta
        mock_datetime_module.timezone = datetime.timezone

        # Mock the now() method on the datetime class inside the mocked module
        mock_now_val = datetime.datetime(2025, 8, 30, 0, 0, 0, tzinfo=datetime.timezone.utc)
        mock_datetime_module.datetime.now.return_value = mock_now_val

        # Mock the WFA configuration
        mock_config.WFA_TOTAL_DAYS = 30
        mock_config.WFA_N_SPLITS = 5
        mock_config.WFA_TRAIN_DAYS = 4
        mock_config.WFA_VALIDATE_DAYS = 2

        # --- Execution ---
        folds = walk_forward._generate_wfa_folds()

        # --- Assertions ---
        self.assertEqual(len(folds), 5)

        # Check the dates of the first fold
        self.assertEqual(folds[0]['train_start'], datetime.datetime(2025, 7, 31, 0, 0, 0, tzinfo=datetime.timezone.utc))
        self.assertEqual(folds[0]['train_end'], datetime.datetime(2025, 8, 4, 0, 0, 0, tzinfo=datetime.timezone.utc))
        self.assertEqual(folds[0]['validate_start'], datetime.datetime(2025, 8, 4, 0, 0, 0, tzinfo=datetime.timezone.utc))
        self.assertEqual(folds[0]['validate_end'], datetime.datetime(2025, 8, 6, 0, 0, 0, tzinfo=datetime.timezone.utc))

        # Check the dates of the second fold to verify the rolling window logic
        self.assertEqual(folds[1]['train_start'], datetime.datetime(2025, 8, 2, 0, 0, 0, tzinfo=datetime.timezone.utc))
        self.assertEqual(folds[1]['train_end'], datetime.datetime(2025, 8, 6, 0, 0, 0, tzinfo=datetime.timezone.utc))

        # Check that the last fold does not exceed the current time
        self.assertLessEqual(folds[-1]['validate_end'], mock_now_val)

    @patch('optimizer.walk_forward.shutil')
    @patch('optimizer.walk_forward._generate_wfa_folds')
    @patch('optimizer.walk_forward.data.export_and_split_data')
    @patch('optimizer.walk_forward.study')
    @patch('optimizer.walk_forward.config')
    def test_run_walk_forward_analysis_success(self, mock_config, mock_study_module, mock_export_data, mock_generate_folds, mock_shutil):
        """
        Tests the full WFA orchestration logic for a successful scenario.
        """
        # --- Setup ---
        # Mock fold generation to return realistic fold dictionaries
        base_time = datetime.datetime.now(datetime.timezone.utc)
        mock_generate_folds.return_value = [
            {'train_start': base_time, 'train_end': base_time, 'validate_end': base_time},
            {'train_start': base_time, 'train_end': base_time, 'validate_end': base_time},
            {'train_start': base_time, 'train_end': base_time, 'validate_end': base_time},
        ]

        # Mock data export to always succeed
        mock_export_data.return_value = (Path('/fake/train.csv'), Path('/fake/validate.csv'))

        # Create a mock study object that has some completed trials
        mock_trial = MagicMock()
        mock_trial.state = optuna.trial.TrialState.COMPLETE
        mock_study = MagicMock()
        mock_study.trials = [mock_trial]
        mock_study_module.create_study.return_value = mock_study

        # Mock the validation result of each fold: 2 pass, 1 fails
        # We now mock the _validate_fold function directly in the walk_forward module
        with patch('optimizer.walk_forward._validate_fold') as mock_validate_fold:
            mock_validate_fold.side_effect = [
                {"status": "passed", "params": {"p1": 1}},
                {"status": "failed", "reason": "test failure"},
                {"status": "passed", "params": {"p1": 3}}, # This is the last successful one
            ]

            # Mock config to require 2/3 folds to pass (66% > 60%)
            mock_config.WFA_MIN_SUCCESS_RATIO = 0.6
            mock_wfa_dir = MagicMock()
            mock_wfa_dir.exists.return_value = True
            mock_config.WFA_DIR.__truediv__.return_value = mock_wfa_dir


            # --- Execution ---
            job = {'n_trials_per_fold': 50}
            result = walk_forward.run_walk_forward_analysis(job)

            # --- Assertions ---
            self.assertTrue(result) # The overall analysis should pass

            # Check that optimization was run for each fold
            self.assertEqual(mock_study_module.run_optimization.call_count, 3)

            # Check that the parameters from the LAST successful fold were saved
            mock_study_module._save_global_best_parameters.assert_called_once_with({"p1": 3})

            # Check that the temporary directory is cleaned up
            mock_shutil.rmtree.assert_called_once_with(mock_wfa_dir)


    @patch('optimizer.walk_forward.shutil')
    @patch('optimizer.walk_forward._generate_wfa_folds')
    @patch('optimizer.walk_forward.data.export_and_split_data')
    @patch('optimizer.walk_forward.study')
    @patch('optimizer.walk_forward.config')
    def test_run_walk_forward_analysis_failure(self, mock_config, mock_study_module, mock_export_data, mock_generate_folds, mock_shutil):
        """
        Tests the full WFA orchestration logic for a failure scenario.
        """
        # --- Setup ---
        base_time = datetime.datetime.now(datetime.timezone.utc)
        mock_generate_folds.return_value = [
            {'train_start': base_time, 'train_end': base_time, 'validate_end': base_time},
            {'train_start': base_time, 'train_end': base_time, 'validate_end': base_time},
            {'train_start': base_time, 'train_end': base_time, 'validate_end': base_time},
        ]
        mock_export_data.return_value = (Path('/fake/train.csv'), Path('/fake/validate.csv'))

        # Create a mock study object that has some completed trials
        mock_trial = MagicMock()
        mock_trial.state = optuna.trial.TrialState.COMPLETE
        mock_study = MagicMock()
        mock_study.trials = [mock_trial]
        mock_study_module.create_study.return_value = mock_study

        # Mock the validation result of each fold: 1 pass, 2 fail
        with patch('optimizer.walk_forward._validate_fold') as mock_validate_fold:
            mock_validate_fold.side_effect = [
                {"status": "passed", "params": {"p1": 1}},
                {"status": "failed", "reason": "test failure"},
                {"status": "failed", "reason": "another failure"},
            ]

            # Mock config to require 2/3 folds to pass (33% < 60%)
            mock_config.WFA_MIN_SUCCESS_RATIO = 0.6
            mock_wfa_dir = MagicMock()
            mock_wfa_dir.exists.return_value = True
            mock_config.WFA_DIR.__truediv__.return_value = mock_wfa_dir

            # --- Execution ---
            job = {'n_trials_per_fold': 50}
            result = walk_forward.run_walk_forward_analysis(job)

            # --- Assertions ---
            self.assertFalse(result) # The overall analysis should fail

            # Check that the save function was NOT called
            mock_study_module._save_global_best_parameters.assert_not_called()

            # Check that the temporary directory is cleaned up
            mock_shutil.rmtree.assert_called_once_with(mock_wfa_dir)

if __name__ == '__main__':
    unittest.main()
