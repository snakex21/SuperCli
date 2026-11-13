"""Runtime configuration for the servecfg development server."""
import os

DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8080
DEFAULT_WORKERS = 4
TOKEN_ENV = "SERVECFG_TOKEN"


def load_token():
    """Return the API token from the environment, or None."""
    return os.environ.get(TOKEN_ENV)
