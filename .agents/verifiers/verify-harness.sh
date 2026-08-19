#!/usr/bin/env bash
# Harness Verifier: Validate the .agents automation itself without Go builds.
# Use after changing rules, scripts, verifiers, or project skills.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

echo "=== Verifying Harness Integrity ==="

echo "[1/4] Parsing shell scripts..."
bash -n .agents/verifiers/*.sh .agents/scripts/*.sh

echo "[2/5] Compiling Python scripts..."
PYTHONPYCACHEPREFIX="${TMPDIR:-/tmp}/dce-harness-pycache" \
  python3 -m py_compile .agents/verifiers/*.py .agents/scripts/*.py

python3 .agents/scripts/check_context_refs.py

echo "[3/5] Checking executable tool entry points..."
python3 .agents/scripts/verify_http_tests.py --help >/dev/null

echo "[4/5] Checking skill metadata..."
for skill in .agents/skills/*/SKILL.md; do
  dir_name="$(basename "$(dirname "${skill}")")"
  name="$(sed -n 's/^name:[[:space:]]*//p' "${skill}" | head -n 1)"
  if [ "${name}" != "${dir_name}" ]; then
    echo "FAIL: skill name '${name}' does not match directory '${dir_name}'" >&2
    exit 1
  fi
  grep -q '^description: ' "${skill}"
done

echo "[5/5] Checking AGENTS entry points to README..."
grep -q '\.agents/README.md' AGENTS.md
test -f .agents/README.md

echo "✅ Harness integrity checks passed."
