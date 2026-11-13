"""Configuration loading."""
import os
import tomllib

CONFIG_FILE = "tinyqueue.toml"
DB_ENV = "TINYQUEUE_DB"


def load(path=CONFIG_FILE):
    """Load settings from tinyqueue.toml; TINYQUEUE_DB overrides the store path."""
    settings = {"store_path": "jobs.jsonl", "poll_interval": 0.5, "max_attempts": 5}
    if os.path.exists(path):
        with open(path, "rb") as f:
            settings.update(tomllib.load(f).get("tinyqueue", {}))
    if os.environ.get(DB_ENV):
        settings["store_path"] = os.environ[DB_ENV]
    return settings
