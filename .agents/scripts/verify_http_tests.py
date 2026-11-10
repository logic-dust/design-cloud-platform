#!/usr/bin/env python3
"""
verify_http_tests.py - Portal API vs .http Test Client Verifier
Reads registered routes from test/portal/portal_routes.txt and verifies
that corresponding endpoints exist in test/portal/*.http test scripts.
Also checks Auth Scope correctness and file domain placement.

用法:
  python3 .agents/scripts/verify_http_tests.py            # 报告模式（始终 exit 0）
  python3 .agents/scripts/verify_http_tests.py --strict   # 门禁模式（缺失/警告时 exit 1）
"""

import argparse
import os
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
API_DIR = REPO_ROOT / "internal/server/portal/api"
HTTP_DIR = REPO_ROOT / "test/portal"

sys.path.insert(0, str(HTTP_DIR))
from portal_routes import http_covers_route, parse_route_templates  # noqa: E402
from http_suite import load_all_http_requests  # noqa: E402


def verify_http_tests(strict=False):
    if not API_DIR.is_dir():
        print(f"Error: {API_DIR} not found.")
        sys.exit(1)

    routes_found = sorted(parse_route_templates())

    print("========================================================================")
    print("  Portal API vs HTTP Test Script Verification")
    print("========================================================================")
    print(f"Found {len(routes_found)} unique route declarations in portal_routes.txt.")

    http_requests = load_all_http_requests()
    auto_requests = [r for r in http_requests if not r.manual]
    print(f"Parsed {len(http_requests)} request block(s) from {HTTP_DIR}/*.http "
          f"({len(auto_requests)} auto, {len(http_requests) - len(auto_requests)} @manual).")

    # 1. Coverage Check (template-aware; includes @manual blocks for strict gate)
    missing = []
    manual_only_routes = []
    auto_covered = 0
    for method, path in routes_found:
        if any(http_covers_route(method, path, r.method, r.path_template) for r in http_requests):
            if any(http_covers_route(method, path, r.method, r.path_template) for r in auto_requests):
                auto_covered += 1
            else:
                manual_only_routes.append((method, path))
            continue
        missing.append((method, path))

    print(
        f"Runtime auto coverage (excludes # @manual): {auto_covered}/{len(routes_found)} route templates."
    )
    if manual_only_routes:
        print(
            f"ℹ️  {len(manual_only_routes)} route template(s) appear only in # @manual blocks "
            f"(make test-api will not hit them):"
        )
        for m, p in manual_only_routes[:12]:
            print(f"   - {m:<6} {p}")
        if len(manual_only_routes) > 12:
            print(f"   ... and {len(manual_only_routes) - 12} more")

    # 1b. Stale check: every .http request must hit a registered route
    stale = []
    for r in http_requests:
        if "/api/" not in r.path_template:
            continue
        if any(http_covers_route(m, p, r.method, r.path_template) for m, p in routes_found):
            continue
        stale.append((r.method, r.path_template))

    # 2. Scope & Location Quality Checks
    warnings = []
    http_files = [f for f in os.listdir(HTTP_DIR) if f.endswith(".http")]
    file_contents = {}
    for f in http_files:
        filepath = HTTP_DIR / f
        with open(filepath, "r", encoding="utf-8") as fh:
            file_contents[f] = fh.read()

    public_file_content = file_contents.get("01-public-api.http", "")
    if "Authorization: Bearer" in public_file_content:
        warnings.append(
            "01-public-api.http contains 'Authorization: Bearer' header! "
            "Public API file should only contain unauthenticated endpoints."
        )

    if "/api/settings/platform" in public_file_content:
        warnings.append(
            "01-public-api.http contains '/api/settings/platform'! "
            "Authenticated settings should be in 16-settings.http."
        )

    method_pattern = re.compile(r"^\s*(GET|POST|PUT|DELETE|PATCH|OPTIONS|HEAD)\s+", re.IGNORECASE)
    delimiter_errors = []
    for f in http_files:
        filepath = HTTP_DIR / f
        with open(filepath, "r", encoding="utf-8") as fh:
            lines = fh.readlines()
        delim_indices = [i for i, line in enumerate(lines) if line.strip().startswith("###")]
        for idx, start_i in enumerate(delim_indices):
            end_i = delim_indices[idx + 1] if idx + 1 < len(delim_indices) else len(lines)
            block_lines = lines[start_i:end_i]
            if not any(method_pattern.match(l) for l in block_lines):
                delimiter_errors.append(f"{f}:{start_i+1}: {lines[start_i].strip()}")

    if delimiter_errors:
        for err in delimiter_errors:
            warnings.append(
                f"Invalid `###` block without HTTP request statement: {err} "
                "(use `#=====` for section or `#-----` for note)"
            )

    if warnings:
        print("\n⚠️  Quality & Scope Warnings:")
        for w in warnings:
            print(f"   - {w}")

    if stale:
        print(f"\n⚠️  {len(stale)} .http request(s) target routes that are not registered:")
        for m, p in sorted(set(stale)):
            print(f"   - {m:<6} {p}")
        missing.extend(sorted(set(stale)))

    if missing:
        print(f"\n⚠️  {len(missing)} API route template(s) missing from test/portal/*.http:")
        for m, p in missing:
            print(f"   - {m:<6} {p}")
    else:
        print("\n✅ All Portal API route templates have matching .http examples!")

    if not missing and not warnings:
        print("✅ Scope & Domain Placement checks passed successfully!")
        print("✅ All `###` delimiters are valid request titles with executable HTTP statements!")

    if strict and (missing or warnings):
        print("\n❌ Strict mode: missing routes or scope warnings block the gate.")
        sys.exit(1)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Verify Portal API routes have .http test coverage."
    )
    parser.add_argument(
        "--strict",
        action="store_true",
        help="exit non-zero when routes are missing or warnings exist",
    )
    args = parser.parse_args()
    verify_http_tests(strict=args.strict)
