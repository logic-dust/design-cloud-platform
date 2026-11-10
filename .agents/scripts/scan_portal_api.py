#!/usr/bin/env python3
"""
scan_portal_api.py - Portal API Structure & Handler Extractor
Extracts all routes, HTTP methods, scope groups, and Handler names from
internal/server/portal/api/*.go to assist in regenerating the docs-site/docs/api/ docs
(_index.md overview, user.md user-facing, admin.md admin-scoped).

Supported source patterns:
  1. Explicit endpoint comments: // GET /api/...  (or POST/PUT/DELETE/PATCH)
  2. Block route comments:       // route: /api/...  followed by a func body
     containing g.METHOD("...", h.handler) calls
"""

import os
import re

API_DIR = "internal/server/portal/api"


def guess_group(full_path):
    """Guess the API scope group for a full path based on prefix rules."""
    if full_path.startswith("/api/admin/audit"):
        return "Audit Admin"
    if full_path.startswith("/api/admin/security"):
        return "Security Admin"
    if full_path.startswith("/api/admin/system"):
        return "System Admin"
    if full_path.startswith(("/api/sessions/login", "/api/version", "/api/release", "/api/nodes/registration")):
        return "Public"
    return "Authenticated"


def parse_all_routes():
    """Extract (method, full_path, handler) from all Go handler files."""
    routes = set()

    for root, _, files in os.walk(API_DIR):
        for file in files:
            if not file.endswith(".go") or file.endswith("_test.go"):
                continue
            filepath = os.path.join(root, file)
            with open(filepath, "r", encoding="utf-8") as f:
                content = f.read()

            # 1. Explicit endpoint comments: // GET /api/...
            explicit = re.findall(r'//\s*(GET|POST|PUT|DELETE|PATCH)\s+(/api/[^\s\(\)]+)', content)
            for m, p in explicit:
                routes.add((m.upper(), p, ""))

            # 2. Block routes: // route: /api/... followed by handler func body.
            #    Split content into per-route blocks (up to the next route comment)
            #    so that nested inline funcs (e.g. g.GET("", func(c) {...})) do
            #    not truncate the block.
            parts = re.split(r'(?=//\s*route:)', content)
            for part in parts:
                m_route = re.match(r'//\s*route:\s*(/api/[^\s\(\)]+)', part)
                if not m_route:
                    continue
                base_path = m_route.group(1).rstrip("/")
                # Direct calls on the group receiver (g.METHOD / h.xxxGroup.METHOD)
                calls = re.findall(r'(?:g|h\.\w+)\.(GET|POST|PUT|DELETE|PATCH)\("([^"]*)"\s*,\s*(?:h\.)?(\w+)?', part)
                for m, sub_p, handler in calls:
                    if sub_p.startswith("/api/"):
                        full = sub_p
                    elif sub_p == "":
                        full = base_path
                    else:
                        sub_clean = sub_p if sub_p.startswith("/") else "/" + sub_p
                        full = base_path if base_path.endswith(sub_clean) else base_path + sub_clean
                    routes.add((m.upper(), full, handler))

                # Sub-group forwarding: xxx := g.Group("/sub") + xxx.METHOD(...)
                sub_groups = re.findall(r'(\w+)\s*:?=\s*g\.Group\("([^"]*)"\)', part)
                for var_name, sub_path in sub_groups:
                    sub_body_matches = re.findall(
                        rf'{re.escape(var_name)}\.(GET|POST|PUT|DELETE|PATCH)\("([^"]*)"\s*,\s*(?:h\.)?(\w+)?',
                        part,
                    )
                    for m, sub_p, handler in sub_body_matches:
                        base = base_path if sub_path in ("", "/") else base_path.rstrip("/") + sub_path
                        if sub_p.startswith("/api/"):
                            full = sub_p
                        elif sub_p == "":
                            full = base
                        else:
                            sub_clean = sub_p if sub_p.startswith("/") else "/" + sub_p
                            full = base if base.endswith(sub_clean) else base + sub_clean
                        routes.add((m.upper(), full, handler))

                # Nested handler forwarding: h.xxxRoutes(g.Group("/sub"))
                nested = re.findall(r'h\.(\w+)\(g\.Group\("([^"]*)"\)\)', part)
                for fn_name, sub_path in nested:
                    fn_blocks = re.findall(
                        rf'func \(h \*?\w+\) {re.escape(fn_name)}\(g \*echo\.Group\) \{{([^}}]+)\}}',
                        content,
                    )
                    for fn_body in fn_blocks:
                        fn_calls = re.findall(r'g\.(GET|POST|PUT|DELETE|PATCH)\("([^"]*)"\s*,\s*(?:h\.)?(\w+)?', fn_body)
                        for m, sub_p, handler in fn_calls:
                            base = (base_path.rstrip("/") + sub_path).rstrip("/")
                            if sub_p.startswith("/api/"):
                                full = sub_p
                            elif sub_p == "":
                                full = base
                            else:
                                sub_clean = sub_p if sub_p.startswith("/") else "/" + sub_p
                                full = base if base.endswith(sub_clean) else base + sub_clean
                            routes.add((m.upper(), full, handler))

    return sorted(routes)


def scan_portal_api():
    if not os.path.exists(API_DIR):
        print(f"Error: {API_DIR} not found.")
        raise SystemExit(1)

    routes = parse_all_routes()

    print("========================================================================")
    print("  Portal API Endpoint Summary Extractor")
    print("========================================================================")
    print(f"Found {len(routes)} endpoint handlers across {API_DIR}/*.go:\n")

    print(f"{'Group':<18} | {'Method':<6} | {'Path':<45} | {'Handler'}")
    print("-" * 100)

    for method, path, handler in routes:
        group = guess_group(path)
        print(f"{group:<18} | {method:<6} | {path:<45} | {handler}")


if __name__ == "__main__":
    scan_portal_api()
