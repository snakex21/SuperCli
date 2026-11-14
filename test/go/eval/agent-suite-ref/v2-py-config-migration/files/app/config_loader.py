"""Load service settings from config/envs/<env>.json (schema 2)."""
import json
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Settings:
    name: str
    timeout_ms: int
    db_url: str
    features: tuple = field(default=())
    log_level: str = "info"


def _timeout_ms(text):
    if text.endswith("ms"):
        return int(text[:-2])
    if text.endswith("s"):
        return int(text[:-1]) * 1000
    raise ValueError("bad timeout %r" % text)


def parse(data):
    if data.get("schema") != 2:
        raise ValueError("unsupported config schema: %r" % data.get("schema", 1))
    db = data["db"]
    return Settings(
        name=data["name"],
        timeout_ms=_timeout_ms(data["timeout"]),
        db_url="postgres://%s:%d/%s" % (db["host"], db["port"], db["name"]),
        features=tuple(data.get("features", [])),
        log_level=data.get("logging", {}).get("level", "info"),
    )


def load(path):
    with open(path, encoding="utf-8") as f:
        return parse(json.load(f))
