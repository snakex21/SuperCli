"""Load and validate config/*.toml. Unknown keys are rejected."""

import os
import tomllib

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

SCHEMA = {
    "server": {"host": str, "port": int},
    "cache": {"backend": str, "ttl_seconds": int, "max_entries": int},
    "rate_limit": {"per_minute": int, "per_hour": int},
    "delivery": {"channels": list, "timeout_seconds": int},
}


class ConfigError(ValueError):
    pass


def load(name="app"):
    with open(os.path.join(ROOT, "config", name + ".toml"), "rb") as f:
        data = tomllib.load(f)
    for section, keys in data.items():
        if section not in SCHEMA:
            raise ConfigError("unknown section [%s]" % section)
        for key, value in keys.items():
            if key not in SCHEMA[section]:
                raise ConfigError("unknown key %s.%s" % (section, key))
            if not isinstance(value, SCHEMA[section][key]):
                raise ConfigError("%s.%s: expected %s" % (section, key, SCHEMA[section][key].__name__))
    return data
