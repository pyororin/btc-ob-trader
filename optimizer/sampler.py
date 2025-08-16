import logging
import numpy as np
import optuna
from optuna.distributions import BaseDistribution, CategoricalDistribution, FloatDistribution, IntDistribution
from optuna.samplers import BaseSampler
from optuna.study import Study
from sklearn.neighbors import KernelDensity
from typing import Any, Dict, List, Optional, Tuple

class KDESampler(BaseSampler):
    """
    A sampler that uses Kernel Density Estimation (KDE) to sample parameters.

    This sampler is designed for the "fine-tuning" phase of a coarse-to-fine
    optimization strategy. It is initialized with a set of high-performing trials
    from a previous "coarse" phase. It then builds a KDE model for numerical
    parameters and a discrete probability distribution for categorical parameters
    to guide the search towards promising areas of the search space.

    Args:
        coarse_trials: A list of `optuna.trial.FrozenTrial` objects from the
                       coarse optimization phase.
        seed: An optional random seed for reproducibility.
    """

    def __init__(self, coarse_trials: List[optuna.trial.FrozenTrial], seed: Optional[int] = None):
        if not coarse_trials:
            raise ValueError("KDESampler requires at least one coarse trial to initialize.")

        self._rng = np.random.RandomState(seed)
        self._coarse_trials = coarse_trials
        self._search_space = self._infer_search_space()
        self._param_names = sorted(self._search_space.keys())
        self._numerical_params = [p for p in self._param_names if not isinstance(self._search_space[p], CategoricalDistribution)]
        self._categorical_params = [p for p in self._param_names if isinstance(self._search_space[p], CategoricalDistribution)]

        self._kde_model = self._fit_kde()
        self._categorical_models = self._fit_categorical()

    def _infer_search_space(self) -> Dict[str, BaseDistribution]:
        """
        Infers the search space by taking the union of distributions from all coarse trials.
        This is crucial for handling conditional parameters that may not be present in every trial.
        """
        search_space: Dict[str, BaseDistribution] = {}
        for trial in self._coarse_trials:
            search_space.update(trial.distributions)
        return search_space

    def _fit_kde(self) -> Optional[KernelDensity]:
        """Fits a KDE model to the numerical parameters of the coarse trials."""
        if not self._numerical_params:
            return None

        points = []
        # We only use trials that contain ALL numerical parameters defined in the full search space.
        # For conditional parameters, we will use a default value (e.g., the mean of observed values)
        # for trials where the parameter is not present. This is a simplification.
        # A more advanced approach could model the conditional distribution itself.

        # Calculate mean for each numerical param to use as a fill value
        param_means = {}
        for param_name in self._numerical_params:
            values = [t.params[param_name] for t in self._coarse_trials if param_name in t.params]
            if values:
                param_means[param_name] = np.mean(values)
            else:
                # If a parameter is never present, we can't model it.
                # We can use the distribution's midpoint as a fallback.
                dist = self._search_space[param_name]
                if isinstance(dist, (FloatDistribution, IntDistribution)):
                    param_means[param_name] = (dist.low + dist.high) / 2.0
                else: # Should not happen
                    param_means[param_name] = 0.0


        for trial in self._coarse_trials:
            point = []
            for param_name in self._numerical_params:
                # Use the parameter's value if present, otherwise use the calculated mean
                value = trial.params.get(param_name, param_means.get(param_name))
                point.append(value)
            points.append(point)

        if not points:
            logging.warning("No valid numerical data points found for fitting KDE.")
            return None

        # Simple bandwidth selection, can be improved with GridSearchCV if needed
        n_samples, n_features = np.array(points).shape
        bandwidth = (n_samples * (n_features + 2) / 4.)**(-1. / (n_features + 4))

        kde = KernelDensity(kernel='gaussian', bandwidth=bandwidth)
        kde.fit(points)
        return kde

    def _fit_categorical(self) -> Dict[str, Tuple[np.ndarray, np.ndarray]]:
        """Fits a discrete probability model for each categorical parameter."""
        models = {}
        for param_name in self._categorical_params:
            distribution = self._search_space[param_name]
            counts = {choice: 0 for choice in distribution.choices}
            for trial in self._coarse_trials:
                if param_name in trial.params:
                    counts[trial.params[param_name]] += 1

            choices = list(counts.keys())
            probabilities = np.array(list(counts.values()), dtype=float) / len(self._coarse_trials)
            models[param_name] = (np.array(choices), probabilities)
        return models

    @property
    def _independent_sampler(self) -> BaseSampler:
        """Returns a fallback sampler for dynamic search spaces."""
        return optuna.samplers.RandomSampler(seed=self._rng.randint(2**32))

    def infer_relative_search_space(
        self, study: Study, trial: optuna.trial.FrozenTrial
    ) -> Dict[str, BaseDistribution]:
        """Infers the search space for a given trial."""
        return self._infer_search_space()

    def sample_relative(
        self, study: Study, trial: optuna.trial.FrozenTrial, search_space: Dict[str, BaseDistribution]
    ) -> Dict[str, Any]:
        """Samples from the relative search space (not supported by this sampler)."""
        # This sampler is not designed for relative sampling.
        # It operates on the full search space defined by the initial coarse trials.
        # We fall back to the independent sampler for safety, though this method
        # should not be called if the user does not define a dynamic search space.
        if search_space:
             return self._independent_sampler.sample_relative(study, trial, search_space)
        return {}


    def sample_independent(
        self,
        study: Study,
        trial: optuna.trial.FrozenTrial,
        param_name: str,
        param_distribution: BaseDistribution,
    ) -> Any:
        """
        Samples a single parameter.

        This method is called once for each parameter in the search space.
        To ensure consistency, we sample all parameters at once on the first call
        for a given trial and cache the results.
        """
        # Since this method is called per-parameter, we generate all samples
        # on the first call for a trial and store them.
        trial_id = trial.number
        if not hasattr(self, '_cached_samples') or self._cached_samples.get('trial_id') != trial_id:
            self._cached_samples = {'trial_id': trial_id, 'params': self._sample_all_params()}

        return self._cached_samples['params'][param_name]

    def _sample_all_params(self) -> Dict[str, Any]:
        """Generates a full set of parameters from the fitted models."""
        params = {}

        # Sample numerical parameters from KDE
        if self._kde_model and self._numerical_params:
            # Generate one sample from the KDE model
            numerical_sample = self._kde_model.sample(1)[0]
            for i, name in enumerate(self._numerical_params):
                dist = self._search_space[name]
                # Clip the sample to be within the distribution's bounds
                # and convert to the correct type (int or float)
                if isinstance(dist, FloatDistribution):
                    val = float(np.clip(numerical_sample[i], dist.low, dist.high))
                elif isinstance(dist, IntDistribution):
                    # For integer distributions, round and then clip
                    val = int(np.round(np.clip(numerical_sample[i], dist.low, dist.high)))
                else:
                    # Should not happen based on our filtering
                    val = numerical_sample[i]
                params[name] = val

        # Sample categorical parameters from discrete distributions
        if self._categorical_models:
            for name, (choices, probs) in self._categorical_models.items():
                # Normalize probabilities to ensure they sum to 1, avoiding floating point errors.
                probs_sum = np.sum(probs)
                if probs_sum > 0:
                    probs /= probs_sum
                else:
                    # If all probabilities are zero, fall back to a uniform distribution.
                    probs = np.full(len(choices), 1.0 / len(choices))

                params[name] = self._rng.choice(choices, p=probs)

        return params
