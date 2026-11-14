"""Prints the effective settings for an environment."""
import sys

from app.config_loader import load


def describe(env):
    s = load("config/envs/%s.json" % env)
    return "%s timeout=%dms db=%s features=%s log=%s" % (s.name, s.timeout_ms, s.db_url, ",".join(s.features), s.log_level)


if __name__ == "__main__":
    print(describe(sys.argv[1] if len(sys.argv) > 1 else "dev"))
