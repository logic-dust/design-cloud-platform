#!/usr/bin/env python3
"""
verify_http_tests.py - Portal API vs .http Test Client Verifier
Analyzes registered routes in internal/server/portal/api/*.go and verifies
that corresponding endpoints exist in test/api/*.http test scripts.
Also checks Auth Scope correctness and file domain placement.

用法:
  python3 .agents/scripts/verify_http_tests.py            # 报告模式（始终 exit 0）
  python3 .agents/scripts/verify_http_tests.py --strict   # 门禁模式（缺失/警告时 exit 1）
"""

import argparse
import os
import re
import sys

API_DIR = "internal/server/portal/api"
HTTP_DIR = "test/api"

def parse_all_routes():
    """Extract (METHOD, full_path) from all Go handler files in API_DIR."""
    routes = set()

    for root, _, files in os.walk(API_DIR):
        for file in files:
            if not file.endswith(".go") or file.endswith("_test.go"):
                continue
            filepath = os.path.join(root, file)
            with open(filepath, "r", encoding="utf-8") as f:
                content = f.read()

            # 1. Match explicit endpoint comments: // GET /api/... or // POST /api/...
            explicit_comments = re.findall(r'//\s*(GET|POST|PUT|DELETE|PATCH)\s+(/api/[^\s\(\)]+)', content)
            for m, p in explicit_comments:
                routes.add((m.upper(), p))

            # 2. Match block routes: // route: /api/... followed by func body with g.GET("..."), g.POST("..."), etc.
            blocks = re.findall(r'//\s*route:\s*(/api/[^\s\(\)]+)[\s\S]*?func\s*\([^)]*\)\s*\w+\s*\([^)]*\)\s*\{([^}]+)\}', content)
            for base_path, body in blocks:
                base_path = base_path.rstrip("/")
                calls = re.findall(r'g\.(GET|POST|PUT|DELETE|PATCH)\("([^"]*)"', body)
                for m, sub_p in calls:
                    # If sub_p is already full path, use it; otherwise append to base_path
                    if sub_p.startswith("/api/"):
                        routes.add((m.upper(), sub_p))
                    elif sub_p == "":
                        routes.add((m.upper(), base_path))
                    else:
                        sub_clean = sub_p if sub_p.startswith("/") else "/" + sub_p
                        # Avoid duplicating if base_path already ends with sub_clean
                        if base_path.endswith(sub_clean):
                            routes.add((m.upper(), base_path))
                        else:
                            routes.add((m.upper(), base_path + sub_clean))

    return sorted(list(routes))

def verify_http_tests(strict=False):
    if not os.path.exists(API_DIR):
        print(f"Error: {API_DIR} not found.")
        sys.exit(1)

    routes_found = parse_all_routes()

    print("========================================================================")
    print("  Portal API vs HTTP Test Script Verification")
    print("========================================================================")
    print(f"Found {len(routes_found)} unique route declarations in {API_DIR}/*.go.")

    # Read all .http files
    http_files = [f for f in os.listdir(HTTP_DIR) if f.endswith(".http")]
    file_contents = {}
    all_http_content = ""
    for f in http_files:
        filepath = os.path.join(HTTP_DIR, f)
        with open(filepath, "r", encoding="utf-8") as fh:
            content = fh.read()
            file_contents[f] = content
            all_http_content += "\n" + content

    # 1. Coverage Check
    missing = []
    for method, path in routes_found:
        regex_path = re.sub(r':\w+', r'[^/\\s]+', path)
        pattern = re.compile(rf'{method}\s+.*?{regex_path}', re.IGNORECASE)
        if not pattern.search(all_http_content):
            missing.append((method, path))

    # 2. Scope & Location Quality Checks
    warnings = []

    # Check 01-public-api.http: Should not contain Authorization header
    public_file_content = file_contents.get("01-public-api.http", "")
    if "Authorization: Bearer" in public_file_content:
        warnings.append("01-public-api.http contains 'Authorization: Bearer' header! Public API file should only contain unauthenticated endpoints.")

    # Check if /api/settings/platform is in 01-public-api.http
    if "/api/settings/platform" in public_file_content:
        warnings.append("01-public-api.http contains '/api/settings/platform'! Authenticated settings should be in 16-settings.http.")

    # 3. HTTP Delimiter Syntax Check: ensure no `###` exists without an HTTP request method
    method_pattern = re.compile(r"^\s*(GET|POST|PUT|DELETE|PATCH|OPTIONS|HEAD)\s+", re.IGNORECASE)
    delimiter_errors = []
    for f in http_files:
        filepath = os.path.join(HTTP_DIR, f)
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
            warnings.append(f"Invalid `###` block without HTTP request statement: {err} (use `#=====` for section or `#-----` for note)")

    # Display Results
    if warnings:
        print("\n⚠️  Quality & Scope Warnings:")
        for w in warnings:
            print(f"   - {w}")

    if missing:
        print(f"\n⚠️  {len(missing)} API route(s) currently missing from test/api/*.http:")
        for m, p in missing:
            print(f"   - {m:<6} {p}")
    else:
        print("\n✅ All Portal API endpoints have test coverage in test/api/*.http!")

    if not missing and not warnings:
        print("✅ Scope & Domain Placement checks passed successfully!")
        print("✅ All `###` delimiters are valid request titles with executable HTTP statements!")

    if strict and (missing or warnings):
        print("\n❌ Strict mode: missing routes or scope warnings block the gate.")
        sys.exit(1)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Verify Portal API routes have .http test coverage.")
    parser.add_argument("--strict", action="store_true",
                        help="exit non-zero when routes are missing or warnings exist")
    args = parser.parse_args()
    verify_http_tests(strict=args.strict)
