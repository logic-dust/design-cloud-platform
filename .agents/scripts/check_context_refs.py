#!/usr/bin/env python3
"""Check local links and repository paths in agent entry files."""
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
FILES = [ROOT / "AGENTS.md", ROOT / ".agents/README.md"]
LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")
BACKTICK = re.compile(r"`((?:\.agents|docs-site|test|internal)/[^`]+)`")


def check(path: Path) -> list[str]:
    errors: list[str] = []
    for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        refs = [(m.group(1), True) for m in LINK.finditer(line)]
        refs.extend((m.group(1), False) for m in BACKTICK.finditer(line))
        for ref, is_link in refs:
            if ref.startswith(("http://", "https://", "mailto:", "#")):
                continue
            target = ref.split("#", 1)[0]
            if not target or "*" in target:
                continue
            resolved = (path.parent / target).resolve() if is_link else (ROOT / target).resolve()
            if not resolved.exists():
                errors.append(f"{path.relative_to(ROOT)}:{lineno}: missing reference {ref}")
    return errors


def main() -> int:
    errors = [err for path in FILES for err in check(path)]
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"Context references valid ({len(FILES)} entry files).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
