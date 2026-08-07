# testdata/

Every JSON file in this directory is generated output from
`scripts/generate_fixtures.py`. Do not hand-edit them.

To add or change a test case, edit the relevant `inputs` list in
`scripts/generate_fixtures.py`, then regenerate:

```
make fixtures
```

or directly:

```
pixi run -e dev python scripts/generate_fixtures.py
```

Commit the regenerated JSON alongside the script change. A hand edit to
one of these files that has no matching entry in the generator's input
list will be silently deleted the next time someone runs `make fixtures`
(this is exactly what happened in OB-079: PR #69 hand-edited
`intent_cases.json` without updating the generator).

CI runs the generator and fails the build with `git diff --exit-code
testdata/` if the checked-in JSON does not match freshly generated
output, so this class of drift is now caught automatically.
