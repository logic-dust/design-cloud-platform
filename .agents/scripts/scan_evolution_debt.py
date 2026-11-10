#!/usr/bin/env python3
"""Heuristic evolution-debt candidates for design-cloud-engine (review only, not findings)."""

from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DEFAULT_SCAN = ROOT / "internal"

SKIP_DIRS = {
    ".git",
    "node_modules",
    "vendor",
    "dist",
    "build",
    "__pycache__",
    ".venv",
    "venv",
}
# Generated / high-noise paths: candidates need manual review before acting.
SKIP_PATH_PARTS = (
    "contract/proto/",
    "_grpc.pb.go",
    ".pb.go",
)

RULES = [
    ("dead_alias", re.compile(r"^\s*type\s+\w+\s*=\s*\w+"), "Dead"),
    ("dead_commented", re.compile(r"^\s*//\s*(func|type|var|const)\s+"), "Dead"),
    (
        "compat_fallback",
        re.compile(r"(?i)\b(fallback|backward[_\s-]?compat|legacy)\b"),
        "Compat",
    ),
    (
        "compat_version_branch",
        re.compile(r"(?i)\b(oldversion|v1alpha|compat[_-]?mode)\b"),
        "Compat",
    ),
    (
        "semantic_magic",
        re.compile(r"(?i)(magic[_\s-]?header|bypass[_-]?auth|hardcoded?\s+uid)"),
        "Semantics",
    ),
]


def should_skip(rel: Path) -> bool:
    s = rel.as_posix()
    return any(part in s for part in SKIP_PATH_PARTS)


def scan(root: Path) -> int:
    count = 0
    print(f"# evolution-debt candidates under {root} (heuristic; confirm in source)")
    for dirpath, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d not in SKIP_DIRS and not d.startswith(".")]
        for name in files:
            if not name.endswith((".go", ".proto")):
                continue
            path = Path(dirpath) / name
            rel = path.relative_to(root)
            if should_skip(rel):
                continue
            try:
                lines = path.read_text(encoding="utf-8", errors="ignore").splitlines()
            except OSError:
                continue
            for i, line in enumerate(lines, 1):
                for label, rx, dim in RULES:
                    if rx.search(line):
                        print(f"{dim}/{label}\t{rel}:{i}\t{line.strip()[:120]}")
                        count += 1
                        break
    print(f"# total={count}")
    return count


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Scan design-cloud-engine internal/ for evolution-debt heuristics."
    )
    parser.add_argument(
        "path",
        nargs="?",
        type=Path,
        default=DEFAULT_SCAN,
        help=f"Scan root (default: {DEFAULT_SCAN.relative_to(ROOT)})",
    )
    args = parser.parse_args()
    root = args.path.resolve()
    if not root.is_dir():
        print(f"error: not a directory: {root}", file=sys.stderr)
        return 1
    scan(root)
    return 0


if __name__ == "__main__":
    sys.exit(main())
