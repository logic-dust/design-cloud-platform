#!/usr/bin/env python3
"""
extract_actions.py - Launcher Operation & Privileged Spec Extractor
Parses internal/execution/actions.go and internal/launcher/launcher.go to extract
all Launcher Action names and privilege levels.
"""

import os
import re

ACTIONS_GO = "internal/execution/actions.go"
LAUNCHER_GO = "internal/launcher/launcher.go"

def extract_launcher_actions():
    if not os.path.exists(ACTIONS_GO) or not os.path.exists(LAUNCHER_GO):
        print("Error: actions.go or launcher.go not found.")
        return

    with open(ACTIONS_GO, "r", encoding="utf-8") as f:
        actions_content = f.read()

    with open(LAUNCHER_GO, "r", encoding="utf-8") as f:
        launcher_content = f.read()

    # Extract Action Name Constants
    action_consts = re.findall(r'Action(\w+)\s*ActionName\s*=\s*"([^"]+)"', actions_content)

    print("========================================================================")
    print("  Launcher Operation & Privilege Level Summary")
    print("========================================================================")
    print(f"Found {len(action_consts)} Launcher Action definitions:\n")

    print(f"{'Go Constant':<30} | {'Action ID':<30} | {'Privileged'}")
    print("-" * 75)

    for const_name, action_id in action_consts:
        # Check if Privileged: true is set in launcher.go
        privileged = "False"
        if f'ActionName: Action{const_name}' in launcher_content or f'"{action_id}"' in launcher_content:
            if re.search(rf'Action{const_name}.*?Privileged:\s*true', launcher_content, re.DOTALL):
                privileged = "True"
        print(f"{const_name:<30} | {action_id:<30} | {privileged}")

if __name__ == "__main__":
    extract_launcher_actions()
