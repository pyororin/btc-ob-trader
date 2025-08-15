import optuna
import numpy as np
import logging
import random
import re
from sklearn.preprocessing import MinMaxScaler

from . import simulation
from . import config
from .utils import nest_params


def calculate_max_drawdown(pnl_history: list[float]) -> tuple[float, float]:
    """
    Calculates the maximum and relative drawdown from a PnL history.

    Args:
        pnl_history: A list of profit and loss values for each trade.

    Returns:
        A tuple containing:
        - Maximum drawdown in absolute terms.
        - Maximum drawdown relative to the final PnL.
    """
    if not pnl_history:
        return 0.0, 0.0

    pnl_array = np.array(pnl_history)
    cumulative_pnl = np.cumsum(pnl_array)
    peak = np.maximum.accumulate(cumulative_pnl)
    drawdown = peak - cumulative_pnl
    max_drawdown = float(np.max(drawdown))

    final_pnl = cumulative_pnl[-1] if len(cumulative_pnl) > 0 else 0
    # Add epsilon to avoid division by zero if final PnL is zero
    relative_drawdown = max_drawdown / (final_pnl + 1e-6) if final_pnl > 0 else 0.0

    return max_drawdown, float(relative_drawdown)


class Objective:
    """
    An objective function class for Optuna optimization.

    This class encapsulates the logic for a single trial, including:
    - Suggesting parameters.
    - Running the simulation.
    - Calculating performance metrics.
    - Implementing pruning logic.
    """
    def __init__(self, study: optuna.Study):
        """
        Initializes the Objective class.

        Args:
            study: The Optuna study object, used to access user attributes
                   like the path to the simulation data CSV.
        """
        self.study = study
        self.phase_name = self.study.user_attrs.get('phase_name', 'unknown')

    def __call__(self, trial: optuna.Trial) -> float:
        """
        Executes a single optimization trial.
        The objective is to maximize a penalized Sharpe Ratio.
        """
        flat_params = self._suggest_parameters(trial)
        params = nest_params(flat_params)

        sim_csv_path = self.study.user_attrs.get('current_csv_path')
        if not sim_csv_path:
            logging.error("Simulation CSV path not found in study user attributes.")
            raise optuna.exceptions.TrialPruned()

        # Run the simulation and get both summary and logs
        summary, stderr_log = simulation.run_simulation(params, sim_csv_path)

        # If the simulation crashes (e.g., Go panic), summary might be None or {}.
        if not summary:
            logging.warning(
                f"Trial {trial.number} failed: simulation returned empty or None summary. "
                f"This may be due to a crash in the simulation binary."
            )
            # Prune the trial to prevent it from being considered a valid candidate.
            raise optuna.exceptions.TrialPruned()

        # Calculate metrics, including those from logs
        self._calculate_and_set_metrics(trial, summary, stderr_log)

        # --- Pruning ---
        total_trades = trial.user_attrs.get("trades", 0)
        if total_trades < config.MIN_TRADES_FOR_PRUNING:
            logging.debug(f"Trial {trial.number} pruned due to insufficient trades: {total_trades} < {config.MIN_TRADES_FOR_PRUNING}")
            raise optuna.exceptions.TrialPruned()

        realization_rate = trial.user_attrs.get("realization_rate", 0.0)
        confirmed_signals = trial.user_attrs.get("confirmed_signals", 0)
        if confirmed_signals > 10 and realization_rate < 0.1:
            logging.debug(f"Trial {trial.number} pruned due to low realization rate: {realization_rate:.2f}")
            raise optuna.exceptions.TrialPruned()

        relative_drawdown = trial.user_attrs.get("relative_drawdown", 1.0)
        if relative_drawdown > config.DD_PENALTY_THRESHOLD:
            logging.debug(f"Trial {trial.number} pruned for high relative drawdown: {relative_drawdown:.2%}")
            raise optuna.exceptions.TrialPruned()

        execution_rate = trial.user_attrs.get("execution_rate", 0.0)
        if execution_rate < 0.5:
            logging.debug(f"Trial {trial.number} pruned due to low execution rate: {execution_rate:.2f}")
            raise optuna.exceptions.TrialPruned()

        # --- Stability Analysis (Objective Regularization) ---
        # This is computationally expensive and should only be run during the fine-tuning phase.
        if self.phase_name != 'coarse-phase':
            jitter_summaries = []
            for i in range(config.STABILITY_CHECK_N_RUNS):
                jittered_flat_params = self._get_jittered_params(trial)
                jittered_nested_params = nest_params(jittered_flat_params)
                jitter_summary, _ = simulation.run_simulation(jittered_nested_params, sim_csv_path)
                if jitter_summary and jitter_summary.get('SharpeRatio') is not None:
                    jitter_summaries.append(jitter_summary)

            if len(jitter_summaries) < config.STABILITY_CHECK_N_RUNS / 2:
                logging.warning(f"Trial {trial.number}: Too many jittered simulations failed. Using original result without penalty.")
                jitter_summaries = [summary]

            sharpe_ratios = [res.get('sharpe_ratio', 0.0) for res in jitter_summaries]
            mean_sr = np.mean(sharpe_ratios)
            std_sr = np.std(sharpe_ratios)

            # The final objective is penalized by the standard deviation of the Sharpe Ratio
            lambda_penalty = config.STABILITY_PENALTY_FACTOR
            final_sr = mean_sr - (lambda_penalty * std_sr)

            # Store metrics derived from stability analysis
            trial.set_user_attr("mean_sharpe_ratio", mean_sr)
            trial.set_user_attr("stdev_sharpe_ratio", std_sr)
            profit_factors = [res.get('profit_factor', 0.0) for res in jitter_summaries]
            sqns = [res.get('sharpe_ratio', 0.0) * np.sqrt(res.get('total_trades', 0)) for res in jitter_summaries]
            trial.set_user_attr("mean_sqn", np.mean(sqns))
            trial.set_user_attr("mean_profit_factor", np.mean(profit_factors))
        else:
            # For the coarse phase, skip stability analysis and use the raw Sharpe Ratio
            final_sr = trial.user_attrs.get("sharpe_ratio", 0.0)
            # Set default values for metrics not calculated in this phase for consistent trial structure
            trial.set_user_attr("mean_sharpe_ratio", final_sr)
            trial.set_user_attr("stdev_sharpe_ratio", 0.0)
            trial.set_user_attr("mean_sqn", trial.user_attrs.get("sqn", 0.0))
            trial.set_user_attr("mean_profit_factor", trial.user_attrs.get("profit_factor", 0.0))

        # --- Final Objective Calculation ---
        # Apply realization and execution rates as direct penalties
        realization_rate = trial.user_attrs.get("realization_rate", 0.0)
        execution_rate = trial.user_attrs.get("execution_rate", 0.0)
        final_sr_penalized = final_sr * realization_rate * execution_rate

        # Add a new penalty for each unrealized trade
        unrealized_trades = trial.user_attrs.get("unrealized_trades", 0)
        sr_penalty_per_unrealized = 0.05
        additive_penalty = unrealized_trades * sr_penalty_per_unrealized
        final_sr_penalized -= additive_penalty

        # Store final, penalized objective values
        trial.set_user_attr("final_sharpe_ratio", final_sr)
        trial.set_user_attr("final_sharpe_ratio_penalized", final_sr_penalized)

        return final_sr_penalized

    def _get_jittered_params(self, trial: optuna.Trial) -> dict:
        """
        Creates a new set of parameters with small random noise applied.
        """
        jittered_params = {}
        for name, value in trial.params.items():
            dist = trial.distributions[name]
            if isinstance(dist, optuna.distributions.FloatDistribution):
                jitter_range = (dist.high - dist.low) * config.STABILITY_JITTER_FACTOR
                noise = random.uniform(-jitter_range, jitter_range)
                new_value = np.clip(value + noise, dist.low, dist.high)
                jittered_params[name] = new_value
            elif isinstance(dist, optuna.distributions.IntDistribution):
                jitter_range = (dist.high - dist.low) * config.STABILITY_JITTER_FACTOR
                noise = random.uniform(-jitter_range, jitter_range)
                new_value = int(round(np.clip(value + noise, dist.low, dist.high)))
                jittered_params[name] = new_value
            else: # CategoricalDistribution
                jittered_params[name] = value
        return jittered_params

    def _suggest_parameters(self, trial: optuna.Trial) -> dict:
        """
        Suggests a flat dictionary of parameters for a trial based on the search
        space defined in the configuration file.
        """
        params = {}
        space = config.PARAMETER_SPACE

        # Define which parameters are conditional
        conditional_params = {
            'dynamic_obi_enabled': ['volatility_factor', 'min_threshold_factor', 'max_threshold_factor']
        }

        # Iterate through the defined parameter space and suggest parameters
        for name, p_space in space.items():
            # Skip conditional parameters for now, they will be handled later
            is_conditional = any(name in group for group in conditional_params.values())
            if is_conditional:
                continue

            p_type = p_space.get('type')
            if p_type == 'int':
                params[name] = trial.suggest_int(name, p_space['low'], p_space['high'])
            elif p_type == 'float':
                log = p_space.get('log', False)
                params[name] = trial.suggest_float(name, p_space['low'], p_space['high'], log=log)
            elif p_type == 'categorical':
                params[name] = trial.suggest_categorical(name, p_space['choices'])
            else:
                logging.warning(f"Unsupported parameter type '{p_type}' for '{name}'")

        # Handle conditional parameters
        if params.get('dynamic_obi_enabled'):
            for name in conditional_params['dynamic_obi_enabled']:
                p_space = space.get(name)
                if not p_space:
                    logging.warning(f"Conditional parameter '{name}' not found in parameter_space config.")
                    continue

                p_type = p_space.get('type')
                if p_type == 'float':
                     log = p_space.get('log', False)
                     params[name] = trial.suggest_float(name, p_space['low'], p_space['high'], log=log)
                elif p_type == 'categorical':
                    params[name] = trial.suggest_categorical(name, p_space['choices'])
                # Add other types if needed for future conditional parameters
                else:
                    logging.warning(f"Unsupported conditional parameter type '{p_type}' for '{name}'")

        return params

    def _calculate_and_set_metrics(self, trial: optuna.Trial, summary: dict, stderr_log: str):
        """Calculates performance metrics and stores them in the trial's user_attrs."""
        # Metrics from JSON summary
        total_trades = summary.get('total_trades', 0)
        sharpe_ratio = summary.get('sharpe_ratio', 0.0)
        profit_factor = summary.get('profit_factor', 0.0)
        win_rate = summary.get('win_rate', 0.0)
        max_drawdown_abs = summary.get('max_drawdown', 0.0)
        pnl_history = summary.get('pnl_history', [])

        # Metrics from parsing stderr logs
        confirmed_signals = len(re.findall(r"Confirmed (LONG|SHORT) signal", stderr_log))
        unrealized_trades = len(re.findall(r"order NOT matched", stderr_log))

        # Calculated metrics
        realization_rate = total_trades / confirmed_signals if confirmed_signals > 0 else 0.0

        executable_signals = total_trades + unrealized_trades
        execution_rate = total_trades / executable_signals if executable_signals > 0 else 0.0

        _, relative_drawdown = calculate_max_drawdown(pnl_history)
        sqn = sharpe_ratio * np.sqrt(total_trades) if total_trades > 0 and sharpe_ratio is not None else 0.0

        # Set attributes
        trial.set_user_attr("trades", total_trades)
        trial.set_user_attr("sharpe_ratio", sharpe_ratio)
        trial.set_user_attr("win_rate", win_rate)
        trial.set_user_attr("profit_factor", profit_factor)
        trial.set_user_attr("max_drawdown", max_drawdown_abs)
        trial.set_user_attr("relative_drawdown", relative_drawdown)
        trial.set_user_attr("sqn", sqn)
        trial.set_user_attr("confirmed_signals", confirmed_signals)
        trial.set_user_attr("realization_rate", realization_rate)
        trial.set_user_attr("unrealized_trades", unrealized_trades)
        trial.set_user_attr("execution_rate", execution_rate)


