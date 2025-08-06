import optuna
from typing import Dict, Any
from optuna.distributions import BaseDistribution, FloatDistribution, IntDistribution, CategoricalDistribution
from .objective import Objective

def get_param_space(use_sensitive_config: bool = False) -> Dict[str, BaseDistribution]:
    """
    Returns the hyperparameter search space for the optimization.

    Args:
        use_sensitive_config: If True, uses a wider, more sensitive search space.
                              Otherwise, uses a safer, more constrained space.

    Returns:
        A dictionary defining the parameter distributions for Optuna.
    """
    if use_sensitive_config:
        # センシティブな設定（広い探索範囲）
        param_space = {
            "entry_threshold": FloatDistribution(0.0, 10.0),
            "exit_threshold": FloatDistribution(0.0, 10.0),
            "leverage": FloatDistribution(1.0, 5.0),
            "stop_loss_threshold": FloatDistribution(0.01, 0.2),
            "take_profit_threshold": FloatDistribution(0.01, 0.2),
            "time_limit_secs": IntDistribution(60, 3600),
            # --- Signal-specific parameters ---
            # OBI (Order Book Imbalance)
            "obi_ema_period": IntDistribution(10, 200),
            # OFI (Order Flow Imbalance)
            "ofi_ema_period": IntDistribution(10, 200),
            # Microprice
            "microprice_ema_period": IntDistribution(10, 200),
            # CVD (Cumulative Volume Delta)
            "cvd_fast_period": IntDistribution(10, 100),
            "cvd_slow_period": IntDistribution(50, 500),
            "cvd_signal_period": IntDistribution(10, 100),
            # Volatility
            "volatility_period": IntDistribution(10, 200),
            "volatility_threshold": FloatDistribution(0.0001, 0.01),
            # Regime
            "regime_period": IntDistribution(10, 200),
            "regime_threshold": FloatDistribution(0.0, 1.0),
        }
    else:
        # 安全な設定（狭い探索範囲）
        param_space = {
            "entry_threshold": FloatDistribution(0.0, 10.0),
            "exit_threshold": FloatDistribution(0.5, 10.0),
            "leverage": FloatDistribution(1.0, 5.0),
            "stop_loss_threshold": FloatDistribution(0.01, 0.2),
            "take_profit_threshold": FloatDistribution(0.01, 0.2),
            "time_limit_secs": IntDistribution(60, 3600),
            # --- Signal-specific parameters ---
            # OBI (Order Book Imbalance)
            "obi_ema_period": IntDistribution(10, 200),
            # OFI (Order Flow Imbalance)
            "ofi_ema_period": IntDistribution(10, 200),
            # Microprice
            "microprice_ema_period": IntDistribution(10, 200),
            # CVD (Cumulative Volume Delta)
            "cvd_fast_period": IntDistribution(10, 100),
            "cvd_slow_period": IntDistribution(50, 500),
            "cvd_signal_period": IntDistribution(10, 100),
            # Volatility
            "volatility_period": IntDistribution(10, 200),
            "volatility_threshold": FloatDistribution(0.0001, 0.01),
            # Regime
            "regime_period": IntDistribution(10, 200),
            "regime_threshold": FloatDistribution(0.0, 1.0),
        }
    return param_space

def run_optimization(study: optuna.Study, csv_path: str, n_trials: int, storage_path: str):
    """
    Runs the hyperparameter optimization using the given study and data.

    Args:
        study: The Optuna study object.
        csv_path: The path to the simulation data CSV file.
        n_trials: The number of trials to run.
        storage_path: The path to the Optuna storage.
    """
    study.set_user_attr('current_csv_path', str(csv_path))
    objective = Objective(study)
    study.optimize(objective, n_trials=n_trials)
