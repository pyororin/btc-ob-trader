import optuna
import numpy as np
import pandas as pd
from scipy.stats import gaussian_kde
import logging
import json
import argparse
from typing import Union, Dict

# Import the centralized config module
from . import config

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')


def analyze_study(study_name: str, storage_url: str) -> Union[Dict, None]:
    """
    Analyzes an Optuna study to find the most robust parameter set.

    This function identifies the best-performing trials and uses Kernel Density
    Estimation (KDE) to find the mode (most likely value) for each hyperparameter
    within that top cohort. This approach helps to find a parameter set that is
    not just a single outlier but represents a region of high performance,
    making it more robust.

    Args:
        study_name: The name of the Optuna study to analyze.
        storage_url: The URL of the storage backend for Optuna (e.g., SQLite URL).

    Returns:
        A dictionary containing the robust parameter set, or None if analysis fails.
    """
    logging.info(f"Loading study '{study_name}' from '{storage_url}'")
    try:
        study = optuna.load_study(study_name=study_name, storage=storage_url)
    except KeyError:
        logging.error(f"Study '{study_name}' not found in the storage.")
        return None

    completed_trials = study.get_trials(deepcopy=False, states=[optuna.trial.TrialState.COMPLETE])
    if not completed_trials:
        logging.warning("No completed trials found in the study.")
        return None

    df = study.trials_dataframe()
    # Filter out pruned or failed trials and drop rows with no objective value
    df = df[df['state'] == 'COMPLETE'].dropna(subset=['value'])

    if df.empty:
        logging.warning("No completed trials with valid objective values found.")
        return None

    # --- 1. Filter top trials based on quantile ---
    quantile_threshold = df['value'].quantile(1 - config.TOP_TRIALS_QUANTILE)
    top_trials_df = df[df['value'] >= quantile_threshold]

    # Safeguard: If not enough top trials, fall back to the single best trial
    if len(top_trials_df) < config.MIN_TRIALS_FOR_ANALYSIS:
        logging.warning(
            f"Number of top trials ({len(top_trials_df)}) is below the minimum "
            f"required for robust analysis ({config.MIN_TRIALS_FOR_ANALYSIS}). "
            "Falling back to the best trial's parameters."
        )
        return study.best_trial.params

    logging.info(f"Analyzing the top {len(top_trials_df)} trials (quantile > {1 - config.TOP_TRIALS_QUANTILE:.2f}).")

    # --- 2. Find the mode for each parameter using KDE for numerical and mode for categorical ---
    robust_params = {}
    param_columns = [col for col in top_trials_df.columns if col.startswith('params_')]

    for param_col in param_columns:
        param_name = param_col.replace('params_', '')
        param_values = top_trials_df[param_col].dropna()

        if param_values.empty:
            logging.warning(f"Parameter '{param_name}' has no valid values in top trials. Skipping.")
            continue

        if pd.api.types.is_numeric_dtype(param_values):
            # Use KDE for numerical parameters
            robust_params[param_name] = _find_mode_kde(param_name, param_values)
        else:
            # Use standard mode for categorical parameters
            robust_params[param_name] = param_values.mode().iloc[0]

    logging.info(f"Found robust parameter set: {robust_params}")
    return robust_params


def _find_mode_kde(param_name: str, param_values: pd.Series) -> Union[float, int]:
    """
    Finds the mode of a numerical parameter series using Gaussian KDE.
    Falls back to median if KDE fails.
    """
    # Check for sufficient variance to apply KDE
    if param_values.nunique() <= 1 or param_values.std() < 1e-6:
        return param_values.iloc[0]

    try:
        kde = gaussian_kde(param_values)
        # Evaluate KDE on a fine grid to find the peak
        grid = np.linspace(param_values.min(), param_values.max(), 500)
        kde_values = kde.evaluate(grid)
        mode_value = grid[np.argmax(kde_values)]

        # For integer parameters, round the mode to the nearest integer
        if pd.api.types.is_integer_dtype(param_values):
            return int(round(mode_value))
        return mode_value

    except (np.linalg.LinAlgError, ValueError) as e:
        # If KDE fails (e.g., singular matrix), fall back to a simpler statistic
        logging.warning(f"KDE failed for {param_name} with error: {e}. Falling back to median.")
        median_value = param_values.median()
        if pd.api.types.is_integer_dtype(param_values):
            return int(round(median_value))
        return median_value


def main():
    """
    Main function to run the analysis from the command line.
    It expects the study name as an argument and prints the resulting
    robust parameters as a JSON string to stdout.
    """
    parser = argparse.ArgumentParser(description="Analyze an Optuna study to find robust parameters.")
    parser.add_argument(
        '--study-name',
        type=str,
        required=True,
        help='The name of the Optuna study to analyze.'
    )
    args = parser.parse_args()

    robust_params = analyze_study(args.study_name, config.STORAGE_URL)

    if robust_params:
        # Output the parameters as JSON to stdout for the calling process
        print(json.dumps(robust_params))
    else:
        logging.error("Could not determine robust parameters.")
        exit(1)


if __name__ == "__main__":
    main()
