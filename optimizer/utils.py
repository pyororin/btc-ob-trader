import numbers

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

    This refactored version builds the dictionary structure explicitly and
    handles potential NumPy types by converting them to native Python types.
    This ensures robustness and prevents serialization issues downstream.

    Args:
        flat_params: A flat dictionary where keys match the keys used in
                     `objective.py`'s `trial.suggest_*` calls. Can contain
                     native Python or NumPy types.

    Returns:
        A nested dictionary with native Python types, matching the YAML structure.
    """
    def get_and_convert(key, target_type):
        """
        Safely gets a value from the flat_params dictionary, converts it to the
        target Python type, and handles missing keys or numpy types.
        """
        value = flat_params.get(key)
        if value is None:
            return None
        # Handle numpy types (e.g., np.int64) by converting them to native types
        if isinstance(value, numbers.Number):
            return target_type(value)
        # Handle boolean types, including np.bool_
        if isinstance(value, (bool, numbers.Integral)): # Catches bool, np.bool_, int
             if target_type == bool:
                 return bool(value)
        return target_type(value)

    # A dictionary mapping leaf keys to their required Python type
    type_map = {
        'spread_limit': int, 'lot_max_ratio': float, 'order_ratio': float,
        'adaptive_position_sizing_enabled': bool, 'adaptive_num_trades': int,
        'adaptive_reduction_step': float, 'adaptive_min_ratio': float,
        'long_tp': int, 'long_sl': int, 'short_tp': int, 'short_sl': int,
        'hold_duration_ms': int, 'obi_weight': float, 'ofi_weight': float,
        'cvd_weight': float, 'micro_price_weight': float, 'composite_threshold': float,
        'slope_filter_enabled': bool, 'slope_period': int, 'slope_threshold': float,
        'ewma_lambda': float, 'dynamic_obi_enabled': bool, 'volatility_factor': float,
        'min_threshold_factor': float, 'max_threshold_factor': float, 'twap_enabled': bool,
        'twap_max_order_size_btc': float, 'twap_interval_seconds': int,
        'twap_partial_exit_enabled': bool, 'twap_profit_threshold': float,
        'twap_exit_ratio': float, 'risk_max_drawdown_percent': int,
        'risk_max_position_ratio': float
    }

    p = lambda key: get_and_convert(key, type_map.get(key, str))

    nested_params = {
        'spread_limit': p('spread_limit'),
        'lot_max_ratio': p('lot_max_ratio'),
        'order_ratio': p('order_ratio'),
        'adaptive_position_sizing': {
            'enabled': p('adaptive_position_sizing_enabled'),
            'num_trades': p('adaptive_num_trades'),
            'reduction_step': p('adaptive_reduction_step'),
            'min_ratio': p('adaptive_min_ratio'),
        },
        'long': { 'tp': p('long_tp'), 'sl': p('long_sl') },
        'short': { 'tp': p('short_tp'), 'sl': p('short_sl') },
        'signal': {
            'hold_duration_ms': p('hold_duration_ms'),
            'obi_weight': p('obi_weight'), 'ofi_weight': p('ofi_weight'),
            'cvd_weight': p('cvd_weight'), 'micro_price_weight': p('micro_price_weight'),
            'composite_threshold': p('composite_threshold'),
            'slope_filter': {
                'enabled': p('slope_filter_enabled'), 'period': p('slope_period'),
                'threshold': p('slope_threshold'),
            },
        },
        'volatility': {
            'ewma_lambda': p('ewma_lambda'),
            'dynamic_obi': {
                'enabled': p('dynamic_obi_enabled'), 'volatility_factor': p('volatility_factor'),
                'min_threshold_factor': p('min_threshold_factor'),
                'max_threshold_factor': p('max_threshold_factor'),
            },
        },
        'twap': {
            'enabled': p('twap_enabled'), 'max_order_size_btc': p('twap_max_order_size_btc'),
            'interval_seconds': p('twap_interval_seconds'),
            'partial_exit_enabled': p('twap_partial_exit_enabled'),
            'profit_threshold': p('twap_profit_threshold'), 'exit_ratio': p('twap_exit_ratio'),
        },
        'risk': {
            'max_drawdown_percent': p('risk_max_drawdown_percent'),
            'max_position_ratio': p('risk_max_position_ratio'),
        },
    }

    def remove_none_recursively(d):
        if not isinstance(d, dict):
            return d
        return {k: remove_none_recursively(v) for k, v in d.items() if v is not None}

    final_params = remove_none_recursively(nested_params)

    # Restore required structure after removing None values
    if 'long' not in final_params: final_params['long'] = {}
    final_params['long']['obi_threshold'] = 0.0
    if 'short' not in final_params: final_params['short'] = {}
    final_params['short']['obi_threshold'] = 0.0
    if 'adaptive_position_sizing' not in final_params: final_params['adaptive_position_sizing'] = {}
    if 'signal' not in final_params: final_params['signal'] = {}
    if 'slope_filter' not in final_params.get('signal', {}): final_params['signal']['slope_filter'] = {}
    if 'volatility' not in final_params: final_params['volatility'] = {}
    if 'dynamic_obi' not in final_params.get('volatility', {}): final_params['volatility']['dynamic_obi'] = {}
    if 'twap' not in final_params: final_params['twap'] = {}
    if 'risk' not in final_params: final_params['risk'] = {}

    return final_params
