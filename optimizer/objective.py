import optuna
import numpy as np
import logging
import random
from sklearn.preprocessing import MinMaxScaler

from . import simulation
from . import config

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
    relative_drawdown = max_drawdown / (final_pnl + 1e-6)

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

    def __call__(self, trial: optuna.Trial) -> tuple[float, float]:
        """
        Executes a single optimization trial for multi-objective optimization.

        This function handles failed or invalid trials by returning a penalty
        value instead of raising an exception, to ensure the MOTPE sampler
        always receives valid objective values.

        Args:
            trial: The Optuna trial object.

        Returns:
            A tuple of objective values (Sharpe Ratio, Max Drawdown) for
            Optuna to optimize.
        """
        # This function returns a value that is guaranteed to be dominated by
        # a trial with 0 trades (SR=0, MaxDD=0). This prevents trials that
        # are pruned for low trade count or high drawdown from bloating the
        # Pareto front.
        def get_dominated_penalty():
            return -1.0, 1_000_000.0

        params = self._suggest_parameters(trial)

        sim_csv_path = self.study.user_attrs.get('current_csv_path')
        if not sim_csv_path:
            logging.error("Simulation CSV path not found in study user attributes. Returning penalty.")
            return get_dominated_penalty()

        summary = simulation.run_simulation(params, sim_csv_path)

        if not isinstance(summary, dict) or not summary:
            logging.warning(f"Trial {trial.number}: Simulation failed or returned empty result. Returning penalty.")
            return get_dominated_penalty()

        self._calculate_and_set_metrics(trial, summary)

        # Pruning based on minimum trade count
        total_trades = trial.user_attrs.get("trades", 0)
        if total_trades < config.MIN_TRADES_FOR_PRUNING:
            logging.debug(f"Trial {trial.number} has {total_trades} trades (min: {config.MIN_TRADES_FOR_PRUNING}). Returning penalty.")
            return get_dominated_penalty()

        # Soft constraint for high drawdown
        # P0: "dd < 25 % ならペナルティ追加" is interpreted as "if relative dd > 25%, penalize"
        DD_PENALTY_THRESHOLD = 0.25
        relative_drawdown = trial.user_attrs.get("relative_drawdown", 1.0)
        if relative_drawdown > DD_PENALTY_THRESHOLD:
            logging.debug(f"Trial {trial.number} penalized for high relative drawdown: {relative_drawdown:.2%}")
            # Return values that are very unattractive for the optimizer
            return get_dominated_penalty()

        # Pruning via trial.report and should_prune() is not straightforward
        # in multi-objective optimization and is therefore disabled. The HyperbandPruner
        # may still prune based on the first objective if it's reported, but
        # we are not reporting intermediate values here.

        sharpe_ratio = trial.user_attrs.get("sharpe_ratio", 0.0)
        max_drawdown = trial.user_attrs.get("max_drawdown", 0.0)

        return sharpe_ratio, max_drawdown

    def _suggest_parameters(self, trial: optuna.Trial) -> dict:
        """Suggests a set of parameters for a trial."""
        return {
            'spread_limit': trial.suggest_int('spread_limit', 20, 80),
            'lot_max_ratio': trial.suggest_float('lot_max_ratio', 0.8, 1.0),
            'order_ratio': trial.suggest_float('order_ratio', 0.8, 1.0),
            'adaptive_position_sizing_enabled': trial.suggest_categorical('adaptive_position_sizing_enabled', [True, False]),
            'adaptive_num_trades': trial.suggest_int('adaptive_num_trades', 3, 20),
            'adaptive_reduction_step': trial.suggest_float('adaptive_reduction_step', 0.5, 1.0),
            'adaptive_min_ratio': trial.suggest_float('adaptive_min_ratio', 0.1, 0.8),
            'long_obi_threshold': trial.suggest_float('long_obi_threshold', 0.05, 4.0),
            'long_tp': trial.suggest_int('long_tp', 50, 200),
            'long_sl': trial.suggest_int('long_sl', -200, -50),
            'short_obi_threshold': trial.suggest_float('short_obi_threshold', -4.0, -0.05),
            'short_tp': trial.suggest_int('short_tp', 50, 200),
            'short_sl': trial.suggest_int('short_sl', -200, -50),
            'hold_duration_ms': trial.suggest_int('hold_duration_ms', 200, 1000),
            'slope_filter_enabled': trial.suggest_categorical('slope_filter_enabled', [True, False]),
            'slope_period': trial.suggest_int('slope_period', 3, 50),
            'slope_threshold': trial.suggest_float('slope_threshold', 0.0, 1.0),
            'ewma_lambda': trial.suggest_float('ewma_lambda', 0.05, 0.3),
            'dynamic_obi_enabled': trial.suggest_categorical('dynamic_obi_enabled', [True, False]),
            'volatility_factor': trial.suggest_float('volatility_factor', 0.25, 10.0),
            'min_threshold_factor': trial.suggest_float('min_threshold_factor', 0.5, 1.0),
            'max_threshold_factor': trial.suggest_float('max_threshold_factor', 1.0, 3.0),
            'twap_enabled': trial.suggest_categorical('twap_enabled', [True, False]),
            'twap_max_order_size_btc': trial.suggest_float('twap_max_order_size_btc', 0.01, 0.1),
            'twap_interval_seconds': trial.suggest_int('twap_interval_seconds', 1, 10),
            'twap_partial_exit_enabled': trial.suggest_categorical('twap_partial_exit_enabled', [True, False]),
            'twap_profit_threshold': trial.suggest_float('twap_profit_threshold', 0.1, 2.0),
            'twap_exit_ratio': trial.suggest_float('twap_exit_ratio', 0.1, 1.0),
            'risk_max_drawdown_percent': trial.suggest_int('risk_max_drawdown_percent', 15, 25),
            'risk_max_position_ratio': trial.suggest_float('risk_max_position_ratio', 0.5, 0.9),
            'composite_threshold': trial.suggest_float('composite_threshold', 0.05, 4.0),
            'obi_weight': trial.suggest_float('obi_weight', 0.1, 2.0),
            'ofi_weight': trial.suggest_float('ofi_weight', 0.0, 2.0),
            'cvd_weight': trial.suggest_float('cvd_weight', 0.0, 2.0),
            'micro_price_weight': trial.suggest_float('micro_price_weight', 0.0, 2.0),
        }

    def _calculate_and_set_metrics(self, trial: optuna.Trial, summary: dict):
        """Calculates performance metrics and stores them in the trial's user_attrs."""
        total_trades = summary.get('TotalTrades', 0)
        sharpe_ratio = summary.get('SharpeRatio', 0.0)
        profit_factor = summary.get('ProfitFactor', 0.0)
        win_rate = summary.get('WinRate', 0.0) # WinRate is already a percentage
        max_drawdown_abs = summary.get('MaxDrawdown', 0.0)
        pnl_history = summary.get('PnlHistory', [])

        # The Python-based relative drawdown calculation might be more reliable
        _, relative_drawdown = calculate_max_drawdown(pnl_history)
        sqn = sharpe_ratio * np.sqrt(total_trades) if total_trades > 0 and sharpe_ratio is not None else 0.0

        trial.set_user_attr("trades", total_trades)
        trial.set_user_attr("sharpe_ratio", sharpe_ratio)
        trial.set_user_attr("win_rate", win_rate)
        trial.set_user_attr("profit_factor", profit_factor)
        trial.set_user_attr("max_drawdown", max_drawdown_abs)
        trial.set_user_attr("relative_drawdown", relative_drawdown)
        trial.set_user_attr("sqn", sqn)


class MetricsCalculator:
    """
    A class to calculate a composite objective score from multiple metrics.

    This class takes multiple performance metrics, scales them to a common
    range (0-1), and then computes a weighted average to determine a final
    score for each trial. This allows for a more holistic evaluation of
    a trial's performance beyond a single metric.
    """
    def __init__(self, weights: dict, completed_trials: list[optuna.Trial]):
        self.weights = weights
        self.trials = completed_trials
        self.metrics_data = self._extract_metrics()
        self.scalers = self._fit_scalers()

    def _extract_metrics(self) -> dict[str, list[float]]:
        """Extracts all relevant metrics from the user_attrs of completed trials."""
        metrics_data = {key: [] for key in self.weights.keys()}
        for trial in self.trials:
            for key in self.weights.keys():
                value = trial.user_attrs.get(key)
                if value is not None:
                    metrics_data[key].append(value)
        return metrics_data

    def _fit_scalers(self) -> dict[str, MinMaxScaler]:
        """Fits MinMaxScaler for each metric to normalize them to a 0-1 range."""
        scalers = {}
        for key, values in self.metrics_data.items():
            scaler = MinMaxScaler()
            if values:
                # Scaler expects a 2D array
                scaler.fit(np.array(values).reshape(-1, 1))
            scalers[key] = scaler
        return scalers

    def calculate_score(self, trial: optuna.Trial) -> float:
        """
        Calculates the final composite score for a single trial.

        Args:
            trial: The trial to be scored.

        Returns:
            The weighted, scaled composite score.
        """
        scaled_values = {}
        for key, scaler in self.scalers.items():
            value = trial.user_attrs.get(key, 0.0)

            # Check if the scaler is fitted
            if hasattr(scaler, 'data_max_') and scaler.data_max_ is not None and scaler.data_max_ != scaler.data_min_:
                scaled_value = scaler.transform(np.array([[value]]))[0][0]
            else:
                # Default value if scaling is not possible (e.g., all values were the same)
                scaled_value = 0.5

            # For drawdown, a smaller value is better. We invert the score.
            if key == 'relative_drawdown':
                scaled_value = 1.0 - scaled_value

            scaled_values[key] = scaled_value

        # Calculate weighted average
        objective_value = 0.0
        total_weight = sum(self.weights.values())

        if total_weight == 0:
            return 0.0

        for key, weight in self.weights.items():
            objective_value += scaled_values.get(key, 0.0) * weight

        return objective_value / total_weight
