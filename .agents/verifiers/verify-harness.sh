#!/usr/bin/env bash
# Harness Verifier: Validate the .agents automation itself without Go builds.
# Use after changing rules, scripts, verifiers, or project skills.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${ROOT_DIR}"

echo "=== Verifying Harness Integrity ==="

echo "[1/7] Parsing shell scripts..."
bash -n .agents/verifiers/*.sh .agents/scripts/*.sh

echo "[2/7] Compiling Python scripts..."
PYTHONPYCACHEPREFIX="${TMPDIR:-/tmp}/dce-harness-pycache" \
  python3 -m py_compile .agents/verifiers/*.py .agents/scripts/*.py

python3 .agents/scripts/check_context_refs.py

echo "[3/7] Architecture verifier self-test (fail-closed + pos/neg fixtures)..."
# The fixture builds a throwaway Go module, so it needs a Go toolchain.
# Harness integrity checks are otherwise Go-free; degrade to a skip (not a
# silent pass of the arch gate itself, which runs via verify-changed/verify-all).
if [ "${GO_CMD:-}" ] && [ -x "${GO_CMD}" ]; then
  python3 .agents/verifiers/verify-arch.py --self-test
elif command -v go >/dev/null 2>&1; then
  python3 .agents/verifiers/verify-arch.py --self-test
else
  echo "   ⚠️  Go toolchain unavailable, skipping arch verifier self-test."
fi

echo "[4/7] Checking executable tool entry points..."
python3 .agents/scripts/verify_http_tests.py --help >/dev/null

echo "[5/7] Portal HTTP route vs .http coverage (strict)..."
python3 .agents/scripts/verify_http_tests.py --strict

echo "[6/7] Checking skill metadata..."
for skill in .agents/skills/*/SKILL.md; do
  dir_name="$(basename "$(dirname "${skill}")")"
  name="$(sed -n 's/^name:[[:space:]]*//p' "${skill}" | head -n 1)"
  if [ "${name}" != "${dir_name}" ]; then
    echo "FAIL: skill name '${name}' does not match directory '${dir_name}'" >&2
    exit 1
  fi
  if ! printf '%s' "${name}" | grep -qE '^[a-z0-9]+(-[a-z0-9]+)*$'; then
    echo "FAIL: skill name '${name}' is not kebab-case" >&2
    exit 1
  fi
  grep -q '^description: ' "${skill}"
done

echo "[7/7] Checking AGENTS entry points to README..."
grep -q '\.agents/README.md' AGENTS.md
test -f .agents/README.md

echo "✅ Harness integrity checks passed."
