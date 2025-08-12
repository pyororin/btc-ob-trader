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

        sharpe_ratios = [res.get('SharpeRatio', 0.0) for res in jitter_summaries]
        mean_sr = np.mean(sharpe_ratios)
        std_sr = np.std(sharpe_ratios)

        # The final objective is penalized by the standard deviation of the Sharpe Ratio
        lambda_penalty = config.STABILITY_PENALTY_FACTOR
        final_sr = mean_sr - (lambda_penalty * std_sr)

        # --- Final Objective Calculation ---
        # Apply realization and execution rates as direct penalties
        final_sr_penalized = final_sr * realization_rate * execution_rate

        # Add a new penalty for each unrealized trade
        unrealized_trades = trial.user_attrs.get("unrealized_trades", 0)
        sr_penalty_per_unrealized = 0.05
        additive_penalty = unrealized_trades * sr_penalty_per_unrealized
        final_sr_penalized -= additive_penalty

        # Store all calculated metrics in user_attrs for later analysis
        trial.set_user_attr("mean_sharpe_ratio", mean_sr)
        trial.set_user_attr("stdev_sharpe_ratio", std_sr)
        trial.set_user_attr("final_sharpe_ratio", final_sr)
        trial.set_user_attr("final_sharpe_ratio_penalized", final_sr_penalized)

        # For compatibility with existing analysis scripts, also store other metrics
        # from the jitter analysis, although they are not used in the objective.
        profit_factors = [res.get('ProfitFactor', 0.0) for res in jitter_summaries]
        sqns = [res.get('SharpeRatio', 0.0) * np.sqrt(res.get('TotalTrades', 0)) for res in jitter_summaries]
        trial.set_user_attr("mean_sqn", np.mean(sqns))
        trial.set_user_attr("mean_profit_factor", np.mean(profit_factors))

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
        Suggests a flat dictionary of parameters for a trial.
        """
        params = {}
        params['spread_limit'] = trial.suggest_int('spread_limit', 20, 80)
        params['long_tp'] = trial.suggest_int('long_tp', 50, 200)
        params['long_sl'] = trial.suggest_int('long_sl', -200, -50)
        params['short_tp'] = trial.suggest_int('short_tp', 50, 200)
        params['short_sl'] = trial.suggest_int('short_sl', -200, -50)
        params['obi_weight'] = trial.suggest_float('obi_weight', 0.8, 1.2)
        params['ofi_weight'] = trial.suggest_float('ofi_weight', 0.8, 1.2)
        params['cvd_weight'] = trial.suggest_float('cvd_weight', 0.0, 1.0)
        params['micro_price_weight'] = trial.suggest_float('micro_price_weight', 0.0, 0.5)
        params['composite_threshold'] = trial.suggest_float('composite_threshold', 0.15, 0.25)
        params['ewma_lambda'] = trial.suggest_float('ewma_lambda', 0.05, 0.25, log=True)
        params['entry_price_offset'] = trial.suggest_float('entry_price_offset', 0, 500)
        params['dynamic_obi_enabled'] = trial.suggest_categorical('dynamic_obi_enabled', [True, False])
        if params['dynamic_obi_enabled']:
            params['volatility_factor'] = trial.suggest_float('volatility_factor', 0.75, 1.5, log=True)
            params['min_threshold_factor'] = trial.suggest_float('min_threshold_factor', 0.5, 1.0)
            params['max_threshold_factor'] = trial.suggest_float('max_threshold_factor', 1.0, 1.5)
        return params

    def _calculate_and_set_metrics(self, trial: optuna.Trial, summary: dict, stderr_log: str):
        """Calculates performance metrics and stores them in the trial's user_attrs."""
        # Metrics from JSON summary
        total_trades = summary.get('TotalTrades', 0)
        sharpe_ratio = summary.get('SharpeRatio', 0.0)
        profit_factor = summary.get('ProfitFactor', 0.0)
        win_rate = summary.get('WinRate', 0.0)
        max_drawdown_abs = summary.get('MaxDrawdown', 0.0)
        pnl_history = summary.get('PnlHistory', [])

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


