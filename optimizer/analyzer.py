import optuna
import numpy as np
import pandas as pd
from scipy.stats import gaussian_kde
import logging
import json
import os
import yaml
from pathlib import Path

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

# --- Constants ---
APP_ROOT = Path('/app')

def load_config():
    """Loads configuration from YAML file."""
    config_path = APP_ROOT / 'config' / 'optimizer_config.yaml'
    if not config_path.exists():
        logging.error(f"Configuration file not found at {config_path}")
        return None
    with open(config_path, 'r') as f:
        return yaml.safe_load(f)

config = load_config()
if config is None:
    exit(1)

STORAGE_URL = os.getenv('STORAGE_URL', f"sqlite:///{APP_ROOT / config['params_dir'] / 'optuna_study.db'}")
ANALYZER_CONFIG = config.get('analyzer', {})
TOP_TRIALS_QUANTILE = ANALYZER_CONFIG.get('top_trials_quantile', 0.1)

def analyze_study(study_name, storage_url):
    """
    Analyzes an Optuna study to find the most robust parameter set.
    """
    logging.info(f"Loading study '{study_name}' from '{storage_url}'")
    try:
        study = optuna.load_study(study_name=study_name, storage=storage_url)
    except KeyError:
        logging.error(f"Study '{study_name}' not found in the storage.")
        return None

    all_trials = study.get_trials(deepcopy=False, states=[optuna.trial.TrialState.COMPLETE])
    if not all_trials:
        logging.warning("No completed trials found in the study.")
        return None

    df = study.trials_dataframe()
    df = df[df['state'] == 'COMPLETE'].dropna(subset=['value'])

    if df.empty:
        logging.warning("No completed trials with valid values found.")
        return None

    # --- 1. Filter top trials ---
    quantile_threshold = df['value'].quantile(1 - TOP_TRIALS_QUANTILE)
    top_trials_df = df[df['value'] >= quantile_threshold]

    # Add a safeguard to ensure there are enough trials for analysis
    MIN_TRIALS_FOR_ANALYSIS = 10  # Set a reasonable minimum
    if len(top_trials_df) < MIN_TRIALS_FOR_ANALYSIS:
        logging.warning(
            f"Number of top trials ({len(top_trials_df)}) is below the minimum required for robust analysis ({MIN_TRIALS_FOR_ANALYSIS}). "
            "Falling back to the best trial's parameters."
        )
        best_trial_params = study.best_trial.params
        return best_trial_params


    if top_trials_df.empty:
        logging.warning(f"No trials found above the {1-TOP_TRIALS_QUANTILE:.0%} quantile. Using the best trial instead.")
        best_trial_params = study.best_trial.params
        return best_trial_params

    logging.info(f"Analyzing the top {len(top_trials_df)} trials (quantile > {1 - TOP_TRIALS_QUANTILE:.2f}).")
    logging.debug(f"Top trials dataframe columns: {top_trials_df.columns.tolist()}")
    logging.debug(f"Top trials dataframe head:\n{top_trials_df.head()}")

    # --- 2. Find the mode for each parameter using KDE ---
    robust_params = {}
    param_columns = [col for col in top_trials_df.columns if col.startswith('params_')]

    for param_col in param_columns:
        param_name = param_col.replace('params_', '')
        param_values = top_trials_df[param_col].dropna()

        if param_values.empty:
            logging.warning(f"Parameter '{param_name}' has no valid values. Skipping.")
            continue

        # Handle categorical vs. numerical parameters
        if pd.api.types.is_numeric_dtype(param_values):
            logging.info(f"Analyzing numerical parameter: {param_name}")
            logging.debug(f"Values for {param_name}:\n{param_values.describe()}")

            # Use KDE for numerical parameters
            # Add a check for standard deviation to avoid errors with KDE
            if param_values.nunique() > 1 and param_values.std() > 1e-6:
                try:
                    # Drop NaNs just in case they slipped through
                    param_values_clean = param_values.dropna()
                    if len(param_values_clean) < 2:
                         raise ValueError("Not enough data points to create a KDE.")
                    kde = gaussian_kde(param_values_clean)
                    # Evaluate KDE on a grid of points
                    grid = np.linspace(param_values_clean.min(), param_values_clean.max(), 500)
                except (np.linalg.LinAlgError, ValueError) as e:
                    # If KDE fails (e.g., singular matrix), fall back to mean or mode
                    logging.warning(f"KDE failed for {param_name} with error: {e}. Falling back to median.")
                    robust_params[param_name] = param_values.median()
                    if pd.api.types.is_integer_dtype(param_values.dropna()):
                        robust_params[param_name] = int(round(robust_params[param_name]))
                    continue

                kde_values = kde.evaluate(grid)
                # Find the value with the highest density
                mode_value = grid[np.argmax(kde_values)]

                # For integer parameters, round the mode to the nearest integer
                if pd.api.types.is_integer_dtype(param_values.dropna()):
                     mode_value = int(round(mode_value))
                robust_params[param_name] = mode_value
            else:
                # If only one unique value, that's the mode
                robust_params[param_name] = param_values.iloc[0]
        else:
            # For categorical parameters, find the most frequent value (mode)
            robust_params[param_name] = param_values.mode().iloc[0]

    logging.info(f"Found robust parameter set: {robust_params}")

    return robust_params


def main():
    """
    Main function to run the analysis.
    Expected to be called with the study name as a command-line argument.
    """
    import argparse
    parser = argparse.ArgumentParser(description="Analyze an Optuna study to find robust parameters.")
    parser.add_argument(
        '--study-name',
        type=str,
        default='obi-scalp-optimization',
        help='The name of the Optuna study to analyze.'
    )
    args = parser.parse_args()

    robust_params = analyze_study(args.study_name, STORAGE_URL)

    if robust_params:
        # Output the parameters as JSON to stdout
        print(json.dumps(robust_params))
    else:
        logging.error("Could not determine robust parameters.")
        exit(1)


if __name__ == "__main__":
    main()
