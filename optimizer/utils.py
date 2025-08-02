def finalize_for_yaml(value):
    """
    Custom Jinja2 finalizer to ensure YAML-compatible output.
    Specifically, it converts Python booleans to lowercase 'true'/'false'.
    """
    if isinstance(value, bool):
        return str(value).lower()
    # Ensure None is rendered as an empty string to avoid YAML errors
    return value if value is not None else ''

def nest_params(flat_params: dict) -> dict:
    """
    Converts a flat dictionary of parameters (from Optuna) into a nested
    dictionary suitable for rendering the trade_config.yaml template.

    This version uses a predefined mapping for clarity and robustness,
    avoiding complex regex or brittle logic.

    It also sanitizes string values that represent booleans (e.g., 'True',
    'false') into actual Python booleans, correcting for potential type
    conversion issues when retrieving parameters from DataFrames or databases.

    Args:
        flat_params: A flat dictionary where keys match the keys used in
                     `objective.py`'s `trial.suggest_*` calls.

    Returns:
        A nested dictionary matching the YAML structure.
    """
    # Initialize the nested structure with default values
    nested_params = {
        'adaptive_position_sizing': {},
        'long': {'obi_threshold': 0.0},
        'short': {'obi_threshold': 0.0},
        'signal': {
            'slope_filter': {}
        },
        'volatility': {
            'dynamic_obi': {}
        },
        'twap': {},
        'risk': {}
    }

    # Map flat keys to their location in the nested dictionary
    # The key is the flat param name, the value is a tuple path.
    key_map = {
        'spread_limit': ('spread_limit',),
        'lot_max_ratio': ('lot_max_ratio',),
        'order_ratio': ('order_ratio',),
        # adaptive_position_sizing
        'adaptive_position_sizing_enabled': ('adaptive_position_sizing', 'enabled'),
        'adaptive_num_trades': ('adaptive_position_sizing', 'num_trades'),
        'adaptive_reduction_step': ('adaptive_position_sizing', 'reduction_step'),
        'adaptive_min_ratio': ('adaptive_position_sizing', 'min_ratio'),
        # long
        'long_obi_threshold': ('long', 'obi_threshold'),
        'long_tp': ('long', 'tp'),
        'long_sl': ('long', 'sl'),
        # short
        'short_obi_threshold': ('short', 'obi_threshold'),
        'short_tp': ('short', 'tp'),
        'short_sl': ('short', 'sl'),
        # signal
        'hold_duration_ms': ('signal', 'hold_duration_ms'),
        'obi_weight': ('signal', 'obi_weight'),
        'ofi_weight': ('signal', 'ofi_weight'),
        'cvd_weight': ('signal', 'cvd_weight'),
        'micro_price_weight': ('signal', 'micro_price_weight'),
        'composite_threshold': ('signal', 'composite_threshold'),
        # signal.slope_filter
        'slope_filter_enabled': ('signal', 'slope_filter', 'enabled'),
        'slope_period': ('signal', 'slope_filter', 'period'),
        'slope_threshold': ('signal', 'slope_filter', 'threshold'),
        # volatility
        'ewma_lambda': ('volatility', 'ewma_lambda'),
        # volatility.dynamic_obi
        'dynamic_obi_enabled': ('volatility', 'dynamic_obi', 'enabled'),
        'volatility_factor': ('volatility', 'dynamic_obi', 'volatility_factor'),
        'min_threshold_factor': ('volatility', 'dynamic_obi', 'min_threshold_factor'),
        'max_threshold_factor': ('volatility', 'dynamic_obi', 'max_threshold_factor'),
        # twap
        'twap_enabled': ('twap', 'enabled'),
        'twap_max_order_size_btc': ('twap', 'max_order_size_btc'),
        'twap_interval_seconds': ('twap', 'interval_seconds'),
        'twap_partial_exit_enabled': ('twap', 'partial_exit_enabled'),
        'twap_profit_threshold': ('twap', 'profit_threshold'),
        'twap_exit_ratio': ('twap', 'exit_ratio'),
        # risk
        'risk_max_drawdown_percent': ('risk', 'max_drawdown_percent'),
        'risk_max_position_ratio': ('risk', 'max_position_ratio'),
    }

    # Sanitize and populate the nested dictionary from the flat parameters
    def _sanitize_bool(val):
        if isinstance(val, str):
            if val.lower() == 'true': return True
            if val.lower() == 'false': return False
        # Handle float/int representations of booleans from analyzer.py
        if isinstance(val, (int, float)):
            if val == 1: return True
            if val == 0: return False
        return val

    for flat_key, value in flat_params.items():
        sanitized_value = _sanitize_bool(value)

        if flat_key in key_map:
            path = key_map[flat_key]
            current_level = nested_params
            for i, part in enumerate(path):
                if i == len(path) - 1:
                    # For keys that are specifically boolean, ensure proper conversion
                    if 'enabled' in flat_key:
                         current_level[part] = bool(sanitized_value)
                    else:
                         current_level[part] = sanitized_value
                else:
                    current_level = current_level.setdefault(part, {})
        # Note: Unmapped keys from flat_params are ignored.

    return nested_params
