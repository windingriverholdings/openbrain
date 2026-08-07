"""Pytest bootstrap: put src/ on sys.path so `import openbrain` resolves.

The package is never pip-installed (editable or otherwise) in the dev
environment; every runtime entry point (systemd units, scripts/setup-
personal.sh) sets PYTHONPATH=<repo>/src explicitly instead. The documented
test invocation (pixi.toml's [feature.dev.tasks] test = "pytest tests/ ...")
carries no such PYTHONPATH, so without this conftest, collection of any test
that imports openbrain.* fails with ModuleNotFoundError before a single test
runs. This mirrors the runtime PYTHONPATH convention for the test process.
"""

from __future__ import annotations

import sys
from pathlib import Path

_SRC = Path(__file__).resolve().parent.parent / "src"
if str(_SRC) not in sys.path:
    sys.path.insert(0, str(_SRC))
