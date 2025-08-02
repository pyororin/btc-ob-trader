import re

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

    Example:
        Input: {'long_tp': 100, 'adaptive_position_sizing_enabled': True}
        Output: {'long': {'tp': 100}, 'adaptive_position_sizing': {'enabled': True}}

    Args:
        flat_params: A flat dictionary where keys may contain underscores
                     indicating nesting.

    Returns:
        A nested dictionary.
    """
    nested_params = {}
    # A regex to split keys into parts for nesting.
    # e.g., 'adaptive_position_sizing_enabled' -> ['adaptive_position_sizing', 'enabled']
    # Handles cases like 'long_tp' -> ['long', 'tp']
    key_pattern = re.compile(r'^(long|short|adaptive_position_sizing|signal|volatility|twap|risk)_(.+)$')

    # Add a mapping for keys that need to be nested but don't fit the general pattern
    # e.g. 'slope_filter_enabled' should go into 'signal.slope_filter.enabled'
    special_nesting_map = {
        'slope_filter_enabled': ('signal', 'slope_filter', 'enabled'),
        'slope_period': ('signal', 'slope_filter', 'period'),
        'slope_threshold': ('signal', 'slope_filter', 'threshold'),
        'dynamic_obi_enabled': ('volatility', 'dynamic_obi', 'enabled'),
        'volatility_factor': ('volatility', 'dynamic_obi', 'volatility_factor'),
        'min_threshold_factor': ('volatility', 'dynamic_obi', 'min_threshold_factor'),
        'max_threshold_factor': ('volatility', 'dynamic_obi', 'max_threshold_factor'),
        'adaptive_position_sizing_enabled': ('adaptive_position_sizing', 'enabled'),
        'adaptive_num_trades': ('adaptive_position_sizing', 'num_trades'),
        'adaptive_reduction_step': ('adaptive_position_sizing', 'reduction_step'),
        'adaptive_min_ratio': ('adaptive_position_sizing', 'min_ratio'),
        'twap_enabled': ('twap', 'enabled'),
        'twap_max_order_size_btc': ('twap', 'max_order_size_btc'),
        'twap_interval_seconds': ('twap', 'interval_seconds'),
        'twap_partial_exit_enabled': ('twap', 'partial_exit_enabled'),
        'twap_profit_threshold': ('twap', 'profit_threshold'),
        'twap_exit_ratio': ('twap', 'exit_ratio'),
        'obi_weight': ('signal', 'obi_weight'),
        'ofi_weight': ('signal', 'ofi_weight'),
        'cvd_weight': ('signal', 'cvd_weight'),
        'micro_price_weight': ('signal', 'micro_price_weight'),
        'composite_threshold': ('signal', 'composite_threshold'),
        'hold_duration_ms': ('signal', 'hold_duration_ms'),
        'ewma_lambda': ('volatility', 'ewma_lambda'),
    }


    # Process all parameters
    for key, value in flat_params.items():
        # Handle special cases first
        if key in special_nesting_map:
            path = special_nesting_map[key]
            current_level = nested_params
            for i, part in enumerate(path):
                if i == len(path) - 1:
                    current_level[part] = value
                else:
                    current_level = current_level.setdefault(part, {})
            continue

        # Handle general cases with regex
        match = key_pattern.match(key)
        if match:
            major_key, minor_key = match.groups()
            if major_key not in nested_params:
                nested_params[major_key] = {}
            nested_params[major_key][minor_key] = value
        else:
            # For keys that are already at the top level
            nested_params[key] = value

    # Ensure all major keys exist even if they have no parameters from the flat dict
    # This is important for the template rendering
    all_major_keys = ['long', 'short', 'adaptive_position_sizing', 'signal', 'volatility', 'twap', 'risk']
    for major_key in all_major_keys:
        if major_key not in nested_params:
            nested_params[major_key] = {}

        # Ensure sub-dicts exist as well
        if major_key == 'signal' and 'slope_filter' not in nested_params['signal']:
            nested_params['signal']['slope_filter'] = {}
        if major_key == 'volatility' and 'dynamic_obi' not in nested_params['volatility']:
            nested_params['volatility']['dynamic_obi'] = {}


    # Manually set the unused obi_thresholds as they are in the template
    nested_params['long'].setdefault('obi_threshold', 0.0)
    nested_params['short'].setdefault('obi_threshold', 0.0)

    return nested_params
