#!/usr/bin/env python3
"""
extract_actions.py - dc-exec operation summary

Lists Action constants registered in dcexec.Handlers and resolves wire IDs from
actions.go plus Privilege from actions_linux.go (ActionSpec.PrivilegedExec).
Direct-only actions (no PrivilegedExec spec) are excluded.
"""

import os
import re
import sys

ACTIONS_GO = "internal/execution/actions.go"
ACTIONS_LINUX_GO = "internal/execution/actions_linux.go"
REGISTRY_GO = "internal/dcexec/registry.go"


def main() -> None:
    root = os.environ.get("REPO_ROOT", os.getcwd())
    actions_go = os.path.join(root, ACTIONS_GO)
    actions_linux = os.path.join(root, ACTIONS_LINUX_GO)
    registry_go = os.path.join(root, REGISTRY_GO)

    for path in (actions_go, actions_linux, registry_go):
        if not os.path.isfile(path):
            print(f"Error: missing {path}", file=sys.stderr)
            raise SystemExit(1)

    actions_content = open(actions_go, encoding="utf-8").read()
    linux_content = open(actions_linux, encoding="utf-8").read()
    registry_content = open(registry_go, encoding="utf-8").read()

    id_by_const: dict[str, str] = {}
    for const_name, action_id in re.findall(
        r"Action(\w+)\s+ActionName\s*=\s*\"([^\"]+)\"", actions_content
    ):
        id_by_const[const_name] = action_id

    handler_consts = re.findall(r"execution\.Action(\w+):", registry_content)
    if not handler_consts:
        print("Error: no handlers found in registry.go", file=sys.stderr)
        raise SystemExit(1)

    rows: list[tuple[str, str, str]] = []
    for const_name in handler_consts:
        action_id = id_by_const.get(const_name)
        if not action_id:
            print(f"Warning: no ActionName for Action{const_name}", file=sys.stderr)
            continue
        priv_match = re.search(
            rf"Action{const_name}:\s*\{{.*?PrivilegedExec:\s*&PrivilegedExecSpec\{{Privilege:\s*(\w+)",
            linux_content,
            re.DOTALL,
        )
        if not priv_match:
            print(
                f"Warning: Action{const_name} in dcexec.Handlers but no PrivilegedExec spec in actions_linux.go",
                file=sys.stderr,
            )
            continue
        privilege = priv_match.group(1).replace("Privilege", "")
        rows.append((const_name, action_id, privilege))

    rows.sort(key=lambda r: r[1])

    print("=" * 72)
    print("  dc-exec handlers (execution.Actions with PrivilegedExec + dcexec.Handlers)")
    print("=" * 72)
    print(f"Found {len(rows)} dc-exec-routed actions:\n")
    print(f"{'Go Constant':<30} | {'Action ID':<30} | {'Privilege'}")
    print("-" * 75)
    for const_name, action_id, privilege in rows:
        print(f"{const_name:<30} | {action_id:<30} | {privilege}")


if __name__ == "__main__":
    main()
