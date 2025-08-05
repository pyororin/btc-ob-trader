import unittest
import optuna
import numpy as np
from typing import Dict, Any, List
from optuna.trial import FrozenTrial
from optuna.distributions import FloatDistribution, IntDistribution, CategoricalDistribution

from .sampler import KDESampler

def create_mock_trial(params: Dict[str, Any], trial_number: int) -> FrozenTrial:
    """Helper function to create a mock FrozenTrial."""
    distributions = {}
    for key, value in params.items():
        if isinstance(value, float):
            distributions[key] = FloatDistribution(low=0.0, high=10.0)
        elif isinstance(value, int):
            distributions[key] = IntDistribution(low=0, high=10)
        elif isinstance(value, str):
            distributions[key] = CategoricalDistribution(choices=['a', 'b', 'c'])

    return FrozenTrial(
        number=trial_number,
        state=optuna.trial.TrialState.COMPLETE,
        value=None, # single objective value
        values=[0.0, 0.0, 0.0], # multi-objective values
        datetime_start=None,
        datetime_complete=None,
        params=params,
        distributions=distributions,
        user_attrs={},
        system_attrs={},
        intermediate_values={},
        trial_id=trial_number, # trial_id is deprecated, but we set it for safety
    )


class TestKDESampler(unittest.TestCase):

    def setUp(self):
        """Set up a list of mock trials for testing."""
        self.mock_trials: List[FrozenTrial] = [
            create_mock_trial({'x': 1.0, 'y': 2, 'z': 'a'}, 0),
            create_mock_trial({'x': 1.2, 'y': 3, 'z': 'b'}, 1),
            create_mock_trial({'x': 0.8, 'y': 1, 'z': 'a'}, 2),
            create_mock_trial({'x': 1.5, 'y': 2, 'z': 'c'}, 3),
            create_mock_trial({'x': 0.5, 'y': 4, 'z': 'b'}, 4),
        ]
        self.search_space = self.mock_trials[0].distributions

    def test_init_with_trials(self):
        """Test that the sampler can be initialized with mock trials."""
        sampler = KDESampler(coarse_trials=self.mock_trials, seed=42)
        self.assertIsNotNone(sampler)
        self.assertIsNotNone(sampler._kde_model)
        self.assertEqual(len(sampler._categorical_models), 1)

    def test_init_raises_error_on_empty_trials(self):
        """Test that ValueError is raised if no trials are provided."""
        with self.assertRaises(ValueError):
            KDESampler(coarse_trials=[])

    def test_sample_independent_returns_correct_types(self):
        """Test that sampled parameters have the correct types."""
        sampler = KDESampler(coarse_trials=self.mock_trials, seed=42)
        study = optuna.create_study()
        trial = study.ask() # Get a trial object to pass to the sampler

        # The sampler samples all params at once, so we call it for each param
        # and then check the types.
        x_sample = sampler.sample_independent(study, trial, 'x', self.search_space['x'])
        y_sample = sampler.sample_independent(study, trial, 'y', self.search_space['y'])
        z_sample = sampler.sample_independent(study, trial, 'z', self.search_space['z'])

        self.assertIsInstance(x_sample, float)
        self.assertIsInstance(y_sample, int)
        self.assertIsInstance(z_sample, str)

    def test_sampled_values_are_within_bounds(self):
        """Test that sampled values respect the distribution's bounds."""
        sampler = KDESampler(coarse_trials=self.mock_trials, seed=42)
        study = optuna.create_study()
        trial = study.ask()

        for _ in range(100): # Sample 100 times to be reasonably sure
            x_dist = self.search_space['x']
            y_dist = self.search_space['y']
            z_dist = self.search_space['z']

            x_sample = sampler.sample_independent(study, trial, 'x', x_dist)
            y_sample = sampler.sample_independent(study, trial, 'y', y_dist)
            z_sample = sampler.sample_independent(study, trial, 'z', z_dist)

            self.assertGreaterEqual(x_sample, x_dist.low)
            self.assertLessEqual(x_sample, x_dist.high)

            self.assertGreaterEqual(y_sample, y_dist.low)
            self.assertLessEqual(y_sample, y_dist.high)

            self.assertIn(z_sample, z_dist.choices)

if __name__ == '__main__':
    unittest.main()
