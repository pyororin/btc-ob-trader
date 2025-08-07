import json
import time
import os
from pathlib import Path
from optimizer.main import run_daemon_job
from optimizer.utils import nest_params # Import the utility function

# --- Configuration ---
APP_ROOT = Path('/app')
PARAMS_DIR = APP_ROOT / 'data' / 'params'
JOB_FILE = PARAMS_DIR / 'optimization_job.json'

def create_job_file():
    """Creates a dummy job file to trigger the optimizer."""
    if not PARAMS_DIR.exists():
        PARAMS_DIR.mkdir(parents=True)

    job_data = {
        "trigger_type": "manual_test",
        "window_is_hours": 4, # Use a small window for faster testing
        "window_oos_hours": 1,
        "timestamp": int(time.time())
    }
    with open(JOB_FILE, 'w') as f:
        json.dump(job_data, f)
    print(f"Created job file at {JOB_FILE}")

def ensure_dummy_app_config():
    """Creates a dummy app_config.yaml for the Go binary if it doesn't exist."""
    go_app_config_path = APP_ROOT / 'config' / 'app_config.yaml'
    if not go_app_config_path.parent.exists():
        go_app_config_path.parent.mkdir(parents=True)

    if not go_app_config_path.exists():
        print(f"Creating dummy Go app config at {go_app_config_path}")
        dummy_config = {
            "log_level": "info",
            "database": {
                "host": "db_host_from_yaml",
                "port": 5432,
                "user": "user_from_yaml",
                "password": "pw_from_yaml",
                "name": "db_from_yaml",
                "sslmode": "disable"
            }
        }
        import yaml
        with open(go_app_config_path, 'w') as f:
            yaml.dump(dummy_config, f)


def ensure_dummy_trade_config():
    """Creates a dummy trade config from the template if it doesn't exist."""
    from optimizer import config as optimizer_config
    from jinja2 import Template

    if not optimizer_config.BEST_CONFIG_OUTPUT_PATH.exists():
        print(f"Creating dummy trade config at {optimizer_config.BEST_CONFIG_OUTPUT_PATH}")
        if not optimizer_config.CONFIG_TEMPLATE_PATH.exists():
            print(f"ERROR: Config template not found at {optimizer_config.CONFIG_TEMPLATE_PATH}")
            return

        with open(optimizer_config.CONFIG_TEMPLATE_PATH, 'r') as f:
            template = Template(f.read())

        # Use some default dummy params (flat structure)
        dummy_flat_params = {
            'spread_limit': 100,
            'long_tp': 150,
            'long_sl': -150,
            'short_tp': 150,
            'short_sl': -150,
            'obi_weight': 1.0,
            'ofi_weight': 1.0,
            'cvd_weight': 0.5,
            'micro_price_weight': 0.1,
            'composite_threshold': 0.15,
            'ewma_lambda': 0.1,
            'dynamic_obi_enabled': True,
            'volatility_factor': 1.0,
            'min_threshold_factor': 0.8,
            'max_threshold_factor': 1.2,
        }
        # Convert flat params to nested structure required by the template
        nested_params = nest_params(dummy_flat_params)

        config_str = template.render(nested_params)
        with open(optimizer_config.BEST_CONFIG_OUTPUT_PATH, 'w') as f:
            f.write(config_str)

def run():
    """Runs the full optimization process for testing."""
    db_host = os.environ.get('DB_HOST')
    if not db_host:
        print("Warning: DB_HOST environment variable not set. It should be 'timescaledb'.")
    else:
        print(f"Using DB_HOST: {db_host}")

    env_path = APP_ROOT / '.env'
    if not env_path.exists():
        if (APP_ROOT / '.env.sample').exists():
            import shutil
            shutil.copy(APP_ROOT / '.env.sample', env_path)
            print("Copied .env.sample to .env")
        else:
            print("Error: .env.sample not found.")
            return

    with open(env_path, 'r') as f:
        for line in f:
            if '=' in line and not line.strip().startswith('#'):
                key, value = line.strip().split('=', 1)
                os.environ[key] = value

    db_user = os.getenv('DB_USER')
    db_password = os.getenv('DB_PASSWORD')
    db_name = os.getenv('DB_NAME')
    db_port = os.getenv('DB_PORT')
    if all([db_user, db_password, db_name, db_host, db_port]):
        os.environ['DATABASE_URL'] = f"postgres://{db_user}:{db_password}@{db_host}:{db_port}/{db_name}?sslmode=disable"
        print(f"Set DATABASE_URL with DB_HOST={db_host}.")
    else:
        print("Error: Could not construct DATABASE_URL. Missing DB variables in .env")
        return

    ensure_dummy_app_config()
    ensure_dummy_trade_config()

    job_data = {
        "trigger_type": "manual_test",
        "window_is_hours": 4,
        "window_oos_hours": 1,
        "timestamp": int(time.time())
    }

    try:
        print("Starting optimizer job...")
        run_daemon_job(job_data)
    except Exception as e:
        print(f"An error occurred during optimization: {e}")
    finally:
        print("Test job finished.")

if __name__ == "__main__":
    run()
