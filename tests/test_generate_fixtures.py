"""Tests for scripts/generate_fixtures.py.

OB-079: PR #69 hand-edited testdata/intent_cases.json to add four new
intent cases without adding the matching inputs to this generator's
hardcoded `inputs` list. The next `make fixtures` regen would have
silently deleted them, because the generator is a full write_text()
rewrite driven only by its own input lists (see the module docstring
on generate_fixtures.py).

This suite pins the fix by regenerating every testdata/*.json file
in-process and asserting the output is byte-identical to the checked-in
copy. Any future hand edit to a fixture file, or drift between the
generator's inputs and its output, fails this test instead of surviving
undetected until the next regen silently overwrites it.
"""

from __future__ import annotations

import importlib.util
import json
import sys
from pathlib import Path
from typing import Any

import pytest

_SCRIPT_PATH = Path(__file__).resolve().parent.parent / "scripts" / "generate_fixtures.py"
_TESTDATA = Path(__file__).resolve().parent.parent / "testdata"


def _load_module() -> Any:
    """Import generate_fixtures.py by path, mirroring the build-brain-viz test."""
    spec = importlib.util.spec_from_file_location("generate_fixtures", _SCRIPT_PATH)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    sys.modules["generate_fixtures"] = module
    spec.loader.exec_module(module)
    return module


gen = _load_module()

# Derived from the generator's own manifest, not hand-maintained here: a new
# testdata/*.json file added to FIXTURE_MANIFEST is picked up automatically,
# so a new generated file cannot slip past this suite the way a hardcoded
# filename list could.
_MANIFEST = gen.FIXTURE_MANIFEST


class TestFixtureManifestMatchesTestdataDirectory:
    """The generator's manifest and testdata/'s actual *.json files must
    name exactly the same set.

    Closes the blind spot the byte-identical check below cannot see on its
    own: a *.json file present on disk but missing from FIXTURE_MANIFEST
    would never be regenerated or diffed by anything, so it could drift
    forever with no test ever touching it. Equally, a manifest entry with no
    file on disk means main() has never been run for it.
    """

    def test_manifest_keys_match_testdata_json_files_on_disk(self) -> None:
        on_disk = {p.name for p in _TESTDATA.glob("*.json")}
        assert set(_MANIFEST.keys()) == on_disk


class TestGeneratedFixturesMatchCheckedInCopy:
    """Every file in the generator's manifest must be byte-identical to a
    fresh regen.

    If this fails, the checked-in JSON was hand-edited (or a prior
    generator run was never committed): edit the generator's inputs list
    and regenerate via `make fixtures`, never hand-edit the JSON.
    """

    @pytest.mark.parametrize("filename", sorted(_MANIFEST))
    def test_regenerated_output_matches_checked_in_file(self, filename: str) -> None:
        checked_in = (_TESTDATA / filename).read_text(encoding="utf-8")
        fresh = json.dumps(_MANIFEST[filename](), indent=2) + "\n"
        assert fresh == checked_in, (
            f"testdata/{filename} does not match a fresh regen. "
            "Add the missing case to the generator's inputs list in "
            "scripts/generate_fixtures.py and run `make fixtures`; "
            "never hand-edit the JSON."
        )


class TestTentRegressionCasesArePresentInGenerator:
    """OB-079 regression: the four PR #69 cases must come out of the generator.

    Pins the exact inputs and classifications so a future edit to the
    generator's inputs list (or the intent classifier) that drops or
    reclassifies one of these is caught here, not just via the
    byte-identical check above.
    """

    def test_tent_cases_present_with_expected_classifications(self) -> None:
        fixtures = gen.generate_intent_fixtures()
        by_input = {f["input"]: f for f in fixtures}

        expected = {
            "look for tent notes": ("search", "tent notes"),
            "notes about tent": ("search", "tent"),
            "anything on tents": ("search", "tents"),
            "tent notes": ("ambiguous", "tent notes"),
        }

        for text, (intent, expected_text) in expected.items():
            assert text in by_input, f"{text!r} missing from generator output"
            assert by_input[text]["expected_intent"] == intent
            assert by_input[text]["expected_text"] == expected_text
