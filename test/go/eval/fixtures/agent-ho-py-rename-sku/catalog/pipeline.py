"""Load pipelines from JSON and run them over records."""

import json
import os

from .transforms import TRANSFORMS

PIPELINE_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "pipelines")


class UnknownStep(KeyError):
    pass


def load(name):
    with open(os.path.join(PIPELINE_DIR, name + ".json"), encoding="utf-8") as f:
        spec = json.load(f)
    for step in spec["steps"]:
        if step not in TRANSFORMS:
            raise UnknownStep(step)
    return spec


def run(spec, records):
    out = []
    for record in records:
        for step in spec["steps"]:
            record = TRANSFORMS[step](record)
        out.append(record)
    return out
