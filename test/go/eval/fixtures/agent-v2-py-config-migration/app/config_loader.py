"""Load service settings from config/envs/<env>.json (schema 1)."""
import json
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Settings:
    name: str
    timeout_ms: int
    db_url: str
    features: tuple = field(default=())
    log_level: str = "info"


def _normalize_db_url(url):
    prefix = "postgres://"
    hostport, name = url[len(prefix):].split("/", 1)
    if ":" not in hostport:
        hostport += ":5432"
    return prefix + hostport + "/" + name


def parse(data):
    features = tuple(f.strip() for f in data.get("features", "").split(",") if f.strip())
    dedup = []
    for f in features:
        if f not in dedup:
            dedup.append(f)
    return Settings(
        name=data["name"],
        timeout_ms=int(data["timeout_ms"]),
        db_url=_normalize_db_url(data["db_url"]),
        features=tuple(dedup),
        log_level=data.get("log_level", "info").lower(),
    )


def load(path):
    with open(path, encoding="utf-8") as f:
        return parse(json.load(f))
