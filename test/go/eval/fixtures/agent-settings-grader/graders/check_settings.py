import json
import sys

cfg = json.load(open("config/app.json", encoding="utf-8"))
problems = []
if cfg["audit"]["retention_days"] != 90:
    problems.append(f"audit.retention_days = {cfg['audit']['retention_days']}")
for section in ("metrics", "logs", "exports"):
    if cfg[section]["retention_days"] != 30:
        problems.append(f"{section}.retention_days changed")
print("\n".join(problems) or "ok")
sys.exit(1 if problems else 0)
