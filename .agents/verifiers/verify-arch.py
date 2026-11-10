#!/usr/bin/env python3
"""Architecture boundary verifier (fail-closed).

Reads .agents/rules/arch-rules.yaml and validates forbidden imports against
the REAL package graph produced by `go list`.

Design rules (see todo/refactor.md phase 0 / section 8.1):

1. FAIL CLOSED. `go list` errors, "matched no packages" and empty enumerations
   are hard failures with the command, package and stderr printed. A failed
   check is NEVER interpreted as "zero dependencies" (the old code swallowed
   CalledProcessError and redirected stderr to /dev/null).
2. PATH-SEGMENT matching. A forbidden path matches the importee exactly or as
   a parent path segment (`X` or `X/...`), never as a substring. This prevents
   `internal/db` from accidentally matching e.g. `internal/dbextra`.
3. EXACT vs SUBTREE selectors. A rule's `package` may use `selector: exact`
   (only that one package) or `selector: subtree` (default, package + all
   children). This lets the server root package be ruled separately from
   `internal/server/portal/**` etc.
4. PRODUCTION vs TEST imports. Default rules only inspect production imports
   (`DEP|P|`). Test files (`DEP|T|` / `DEP|X|`) are reported only when a rule
   sets `scope: tests` or `scope: all`, so external test mocks cannot trip a
   production boundary rule.
5. SELF-TEST. `--self-test` builds a throwaway Go module with positive and
   negative fixtures (violating import, path-segment near-miss, test-only
   import, exact/subtree selector, and a forced `go list` failure) and asserts
   the verifier's behavior without touching the repository.

Rule schema (backward compatible with the existing arch-rules.yaml):

    rules:
      - package: "internal/db"
        selector: subtree          # optional: exact | subtree (default subtree)
        scope: production          # optional: production | tests | all
        forbidden_imports: ["internal/server", "testing"]
        reason: "..."

    pattern_rules:                 # unchanged text-pattern check
      - package: "..."
        forbidden_patterns: [...]
"""
import argparse
import os
import shutil
import subprocess
import sys
import tempfile

MODULE = ""
ROOT = ""
GO_CMD = "go"

# One `go list` invocation per rule produces:
#   PKG|<importpath>                       for every package actually enumerated
#   DEP|<kind>|<importer>|<importee>       P=production, T=in-package test, X=external test
#
# PKG lines let us detect "matched no packages" (rc=0 but zero lines), which
# the old implementation silently treated as a passing empty dependency set.
GO_LIST_TMPL = (
    'PKG|{{.ImportPath}}{{printf "\\n"}}'
    '{{$p := .ImportPath}}'
    '{{range .Imports}}DEP|P|{{$p}}|{{.}}{{printf "\\n"}}{{end}}'
    '{{range .TestImports}}DEP|T|{{$p}}|{{.}}{{printf "\\n"}}{{end}}'
    '{{range .XTestImports}}DEP|X|{{$p}}|{{.}}{{printf "\\n"}}{{end}}'
)


class GoListError(Exception):
    """Raised whenever dependency enumeration cannot be trusted."""


def get_module_name(root):
    try:
        with open(os.path.join(root, "go.mod"), "r") as f:
            for line in f:
                if line.startswith("module "):
                    return line.split()[1].strip()
    except OSError:
        pass
    return ""


def parse_rules(root):
    """Parse the small YAML subset used by arch-rules.yaml.

    Supports inline lists, multi-line `- "..."`, and per-rule optional fields
    `selector` (exact|subtree) and `scope` (production|tests|all).
    """
    rules = []
    pattern_rules = []
    current = None
    in_imports = False
    in_patterns = False
    section = None

    yaml_path = os.path.join(root, ".agents/rules/arch-rules.yaml")
    if not os.path.exists(yaml_path):
        print(f"⚠️  {yaml_path} not found, skipping architecture bounds check.")
        return rules, pattern_rules

    with open(yaml_path, "r") as f:
        for line in f:
            s = line.strip()
            if not s or s.startswith('#'):
                continue
            if s == 'rules:':
                section = 'rules'
                continue
            if s == 'pattern_rules:':
                section = 'pattern_rules'
                continue
            if s.startswith('- package:'):
                pkg = s.split(':', 1)[1].strip().strip('"\'')
                if section == 'pattern_rules':
                    current = {'package': pkg, 'forbidden_patterns': [], 'reason': ''}
                    pattern_rules.append(current)
                else:
                    current = {
                        'package': pkg,
                        'forbidden_imports': [],
                        'selector': 'subtree',
                        'scope': 'production',
                        'reason': '',
                    }
                    rules.append(current)
                in_imports = in_patterns = False
                continue
            if current is None:
                continue
            if s.startswith('forbidden_imports:'):
                _, _, rest = s.partition(':')
                rest = rest.strip()
                if rest.startswith('[') and rest.endswith(']'):
                    for item in rest[1:-1].split(','):
                        item = item.strip().strip('"\'')
                        if item:
                            current['forbidden_imports'].append(item)
                    in_imports = False
                else:
                    in_imports = True
                in_patterns = False
            elif s.startswith('forbidden_patterns:'):
                _, _, rest = s.partition(':')
                rest = rest.strip()
                if rest.startswith('[') and rest.endswith(']'):
                    for item in rest[1:-1].split(','):
                        item = item.strip().strip('"\'')
                        if item:
                            current['forbidden_patterns'].append(item)
                    in_patterns = False
                else:
                    in_patterns = True
                in_imports = False
            elif s.startswith('- "') or s.startswith("- '"):
                imp = s[1:].strip().strip('"\'')
                if in_imports:
                    current['forbidden_imports'].append(imp)
                elif in_patterns:
                    current['forbidden_patterns'].append(imp)
            elif s.startswith('selector:'):
                val = s.split(':', 1)[1].strip().strip('"\'')
                if val not in ('exact', 'subtree'):
                    print(f"❌ Invalid selector '{val}' in rule for [{current['package']}]")
                    sys.exit(2)
                current['selector'] = val
                in_imports = in_patterns = False
            elif s.startswith('scope:'):
                val = s.split(':', 1)[1].strip().strip('"\'')
                if val not in ('production', 'tests', 'all'):
                    print(f"❌ Invalid scope '{val}' in rule for [{current['package']}]")
                    sys.exit(2)
                current['scope'] = val
                in_imports = in_patterns = False
            elif s.startswith('reason:'):
                current['reason'] = s.split(':', 1)[1].strip().strip('"\'')
                in_imports = in_patterns = False

    # Fail closed on malformed rules: a rule missing its forbidden list would
    # silently check nothing. Report the offending section instead of crashing
    # later with a KeyError traceback.
    for r in rules:
        if 'forbidden_imports' not in r:
            print(f"❌ GATE CONFIG ERROR: rules entry [{r.get('package')}] has no "
                  f"forbidden_imports (check it is placed under the 'rules:' section).")
            sys.exit(2)
    for r in pattern_rules:
        if 'forbidden_patterns' not in r:
            print(f"❌ GATE CONFIG ERROR: pattern_rules entry [{r.get('package')}] has no "
                  f"forbidden_patterns.")
            sys.exit(2)
    return rules, pattern_rules


def go_list(pkg_pattern, cwd):
    """Return (packages, deps). Raise GoListError on ANY unreliable result."""
    cmd = [GO_CMD, "list", "-f", GO_LIST_TMPL, pkg_pattern]
    try:
        proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=300)
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise GoListError(f"command could not run: {exc}") from exc

    stderr = (proc.stderr or "").strip()
    if proc.returncode != 0:
        raise GoListError(
            f"exit code {proc.returncode}; stderr: {stderr or '(empty)'}"
        )
    # rc=0 but no packages: go prints a "matched no packages" warning. The old
    # code swallowed this via 2>/dev/null and called it a clean pass.
    if "matched no packages" in stderr:
        raise GoListError(f"no packages matched; stderr: {stderr}")

    packages, deps = [], []
    for line in proc.stdout.splitlines():
        if line.startswith("PKG|"):
            packages.append(line[4:])
        elif line.startswith("DEP|"):
            _, kind, importer, importee = line.split("|", 3)
            deps.append((kind, importer, importee))

    if not packages:
        raise GoListError(
            "enumeration returned zero PKG records (a rule targeting a path "
            f"that no longer exists would otherwise pass silently); stderr: {stderr or '(empty)'}"
        )
    return packages, deps


def source_import_path(pkg, module):
    """Resolve a rule's SOURCE package selector to a full import path.

    Source selectors always name a package inside this module (repo-relative
    like `internal/db`, or already-qualified like `<module>/internal/db`).
    A bare `internal` therefore MUST become `<module>/internal`; treating it
    as a stdlib name would make `go list internal/...` enumerate the Go
    toolchain's own internal packages (internal/testenv, internal/cgrouptest...)
    and produce phantom violations.
    """
    if pkg == module or pkg.startswith(module + "/"):
        return pkg
    return f"{module}/{pkg}"


def target_import_path(target, module):
    """Resolve a FORBIDDEN target to a full import path.

    Targets may be repo-relative (`internal/db`), already-qualified
    (`<module>/internal/db` or an external module path), or a stdlib bare
    name (`testing`, the `internal` production-code rule).
    """
    if target == module or target.startswith(module + "/"):
        return target
    if "/" not in target:
        return target  # stdlib bare name such as "testing"
    first = target.split("/", 1)[0]
    if "." in first:
        return target  # already a fully qualified external import path
    return f"{module}/{target}"


def path_segment_matches(importee, forbidden):
    """Exact match or parent path segment — never a substring."""
    return importee == forbidden or importee.startswith(forbidden + "/")


def scope_allows(scope, kind):
    if scope == 'all':
        return True
    if scope == 'tests':
        return kind in ('T', 'X')
    return kind == 'P'  # production (default)


def check_rules(rules, cwd, module, verbose=True):
    """Return (violations, errors). `errors` are fail-closed gate failures."""
    violations = []
    errors = []
    for rule in rules:
        pkg = rule['package']
        full_pkg = source_import_path(pkg, module)
        pattern = full_pkg if rule['selector'] == 'exact' else full_pkg + "/..."
        cmd_str = f"{GO_CMD} list -f <template> {pattern}"
        try:
            packages, deps = go_list(pattern, cwd)
        except GoListError as exc:
            errors.append(
                f"❌ GATE FAILURE: could not enumerate [{pattern}] (selector={rule['selector']})\n"
                f"   command: {cmd_str}\n"
                f"   reason:  {exc}\n"
                f"   A failed check is NOT a passing check. Fix go/toolchain/module state first."
            )
            continue

        if verbose and not rule['forbidden_imports']:
            continue
        for forbidden in rule['forbidden_imports']:
            full_forbidden = target_import_path(forbidden, module)
            matches = []
            for kind, importer, importee in deps:
                if not scope_allows(rule['scope'], kind):
                    continue
                if path_segment_matches(importee, full_forbidden):
                    matches.append((kind, importer, importee))
            if matches:
                violations.append({
                    'rule': rule,
                    'pattern': pattern,
                    'forbidden': full_forbidden,
                    'packages': packages,
                    'matches': matches,
                })
    return violations, errors


def check_pattern_rules(pattern_rules, root):
    """Text-pattern check on non-test .go files (unchanged semantics)."""
    violation = False
    for rule in pattern_rules:
        pkg_dir = os.path.join(root, rule['package'])
        if not os.path.isdir(pkg_dir):
            continue
        for cur, _, files in os.walk(pkg_dir):
            for fname in files:
                if not fname.endswith('.go') or fname.endswith('_test.go'):
                    continue
                fpath = os.path.join(cur, fname)
                try:
                    with open(fpath, 'r', errors='replace') as f:
                        content = f.read()
                except OSError:
                    continue
                for pattern in rule['forbidden_patterns']:
                    if pattern in content:
                        print(f"❌ PATTERN VIOLATION: [{rule['package']}] must NOT contain [{pattern}]")
                        print(f"   Found in: {os.path.relpath(fpath, root)}")
                        if rule.get('reason'):
                            print(f"   Reason: {rule['reason']}")
                        print()
                        violation = True
    return violation


def report(violations, errors):
    for err in errors:
        print(err)
        print()
    for v in violations:
        rule = v['rule']
        print(f"❌ ARCH VIOLATION: [{v['pattern']}] (selector={rule['selector']}, "
              f"scope={rule['scope']}) must NOT import [{v['forbidden']}]")
        for kind, importer, importee in sorted(set(v['matches'])):
            print(f"   Found in: {importer} => {importee}  (kind={kind})")
        if rule.get('reason'):
            print(f"   Reason: {rule['reason']}")
        print()
    if errors or violations:
        print("❌ Architecture boundary check FAILED "
              f"({len(violations)} violation(s), {len(errors)} gate failure(s)).")
        return 1
    print("✅ Architecture boundaries respected (fail-closed go list verification).")
    return 0


# ---------------------------------------------------------------------------
# Self-test: positive and negative fixtures in a throwaway Go module
# ---------------------------------------------------------------------------
SELFTEST_RULES = """\
rules:
  - package: "example.com/archfixture/a"
    forbidden_imports: ["example.com/archfixture/forbidden"]
    reason: "fixture: a does not import forbidden"
  - package: "example.com/archfixture/a"
    forbidden_imports: ["example.com/archfixture/b"]
    reason: "fixture: a imports b (violation)"
  - package: "example.com/archfixture/c"
    forbidden_imports: ["example.com/archfixture/b"]
    reason: "fixture: c imports bx only (path-segment near-miss)"
  - package: "example.com/archfixture/g"
    selector: exact
    forbidden_imports: ["example.com/archfixture/forbidden"]
    reason: "fixture: g itself does not import forbidden, child g/sub does"
  - package: "example.com/archfixture/t"
    forbidden_imports: ["example.com/archfixture/forbidden"]
    reason: "fixture: only an external test imports forbidden (production rule)"
  - package: "example.com/archfixture/t"
    scope: tests
    forbidden_imports: ["example.com/archfixture/forbidden"]
    reason: "fixture: test-scoped rule should catch it"
  - package: "example.com/archfixture/does_not_exist"
    forbidden_imports: ["example.com/archfixture/b"]
    reason: "fixture: nonexistent package must fail closed"
"""


def build_selftest_module(root):
    """Create a tiny module with known import edges; return its dir."""
    mod = "example.com/archfixture"
    with open(os.path.join(root, "go.mod"), "w") as f:
        f.write(f"module {mod}\n\ngo 1.21\n")
    files = {
        "a/a.go": 'package a\nimport (\n _ "example.com/archfixture/b"\n _ "example.com/archfixture/bx"\n)\n',
        "b/b.go": "package b\n",
        "bx/bx.go": "package bx\n",
        "c/c.go": 'package c\nimport _ "example.com/archfixture/bx"\n',
        "forbidden/f.go": "package forbidden\n",
        "g/g.go": "package g\n",
        "g/sub/sub.go": 'package sub\nimport _ "example.com/archfixture/forbidden"\n',
        "t/t.go": "package t\n",
        "t/t_test.go": 'package t_test\nimport _ "example.com/archfixture/forbidden"\n',
    }
    for rel, content in files.items():
        path = os.path.join(root, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(content)
    return mod


def run_self_test():
    """Assert verifier semantics with fixtures. Returns process exit code."""
    tmp = tempfile.mkdtemp(prefix="dce-arch-selftest-")
    ok = True
    try:
        rules_dir = os.path.join(tmp, ".agents/rules")
        os.makedirs(rules_dir, exist_ok=True)
        with open(os.path.join(rules_dir, "arch-rules.yaml"), "w") as f:
            f.write(SELFTEST_RULES)
        module = build_selftest_module(tmp)
        rules, _ = parse_rules(tmp)
        violations, errors = check_rules(rules, tmp, module, verbose=False)

        def has_violation(reason_fragment):
            return any(reason_fragment in v['rule']['reason'] for v in violations)

        def has_error(pkg_fragment):
            return any(pkg_fragment in e for e in errors)

        checks = [
            ("source resolution: bare relative package gets module prefix",
             source_import_path("internal", module) == module + "/internal"),
            ("source resolution: already-qualified package is left alone",
             source_import_path(module + "/internal/db", module) == module + "/internal/db"),
            ("target resolution: stdlib bare name stays bare",
             target_import_path("testing", module) == "testing"),
            ("target resolution: qualified external path stays qualified",
             target_import_path("example.com/ext/pkg", module) == "example.com/ext/pkg"),
            ("positive: a does not import forbidden -> no violation",
             not has_violation("a does not import forbidden")),
            ("negative: a imports b -> violation detected",
             has_violation("a imports b (violation)")),
            ("path segment: c imports bx, forbidden is b -> no false positive",
             not has_violation("path-segment near-miss")),
            ("exact selector: g/sub violation excluded for exact g",
             not has_violation("g itself does not import forbidden")),
            ("production scope: external test import does not trip rule",
             not has_violation("production rule")),
            ("tests scope: test-only import is caught",
             has_violation("test-scoped rule should catch it")),
            ("fail closed: nonexistent package produces a gate error",
             has_error("does_not_exist")),
        ]
        print("=== verify-arch.py self-test (positive/negative fixtures) ===")
        for name, passed in checks:
            print(f"  {'✅' if passed else '❌'} {name}")
            ok = ok and passed
        # Also verify a real go list failure is fail-closed: point GO_CMD at a
        # binary that always exits 1 and assert go_list() raises GoListError
        # (the old implementation swallowed CalledProcessError and passed).
        global GO_CMD
        saved = GO_CMD
        try:
            GO_CMD = "/bin/false"
            try:
                go_list(module + "/...", tmp)
                raised = False
            except GoListError:
                raised = True
            print(f"  {'✅' if raised else '❌'} fail closed: go list error raises GoListError (not swallowed)")
            ok = ok and raised
        finally:
            GO_CMD = saved
        print(f"=== self-test {'PASSED' if ok else 'FAILED'} ===")
        return 0 if ok else 1
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    global MODULE, ROOT, GO_CMD
    ap = argparse.ArgumentParser(description="Fail-closed architecture boundary verifier")
    ap.add_argument("--self-test", action="store_true",
                    help="run positive/negative fixtures against a throwaway module")
    ap.add_argument("--repo-root", default=None,
                    help="repository root (default: current working directory)")
    args = ap.parse_args()

    if args.self_test:
        ROOT = os.getcwd()
        return run_self_test()

    ROOT = os.path.abspath(args.repo_root or os.getcwd())
    MODULE = get_module_name(ROOT)
    if not MODULE:
        print(f"❌ Could not find module name in {ROOT}/go.mod")
        return 1

    rules, pattern_rules = parse_rules(ROOT)
    violations, errors = check_rules(rules, ROOT, MODULE)
    rc = report(violations, errors)
    if check_pattern_rules(pattern_rules, ROOT):
        rc = 1
    return rc


if __name__ == "__main__":
    sys.exit(main())
