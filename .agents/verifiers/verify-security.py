#!/usr/bin/env python3
"""
verify-security.py - Security Red-Line Static Scanner
对应 .agents/rules/security-rules.md 的"自动阻断触发条件"表，对工作区做静态扫描。

检测项:
  1. 硬编码凭据: password/passwd/secret/api_key 等被赋值为非空字符串字面量（排除测试 mock 与 struct tag）→ 阻断
  2. 敏感属性日志: slog 属性键为 password/secret 等，把敏感值写入日志 → 阻断
  3. 命令注入候选: exec.Command/CommandContext 参数中出现字符串拼接（+）→ 阻断
  4. 路径穿越候选: os.* 文件操作中出现字符串拼接（+）→ 阻断；
     filepath.Join/Clean/Abs 内出现字符串拼接（+）→ 警告（Join 会规范化路径，需人工审查）

扫描范围: internal/ 与 cmd/ 下的非 _test.go 文件。
任何阻断级发现都会以非零退出码阻断（自动阻断触发条件）。

用法:
  ./.agents/verifiers/verify-security.py          # 全量扫描（默认）
"""

import os
import re
import sys

SCAN_DIRS = ("internal", "cmd")
# 敏感词（支持驼峰子串匹配）。注意必须用 (?:...) 包裹，否则 | 优先级会拆散模式
SENSITIVE_KEYS = r"(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token)"
TAG_PREFIXES = ("json:", "gorm:", "validate:", "yaml:", "toml:", "binding:",
                "form:", "query:", "header:", "env:", "xml:")
# 以这些后缀结尾的标识符通常是路径/文件名/哈希，而非凭据本身
SAFE_IDENT_SUFFIXES = ("path", "file", "dir", "hash", "hashed", "filepath",
                       "dirpath", "filename", "dirname")

HARDCODED_CRED = re.compile(
    rf"\b\w*{SENSITIVE_KEYS}\w*\b\s*[:=]\s*\"([^\"\s]{{6,}})\"", re.IGNORECASE
)
SENSITIVE_LOG_ATTR = re.compile(
    rf"\.(?:String|Any|Int|Int64|Uint|Uint64|Bool|Float64|Time|Duration|Group)"
    rf"\(\s*\"({SENSITIVE_KEYS})\"\s*,", re.IGNORECASE
)
EXEC_CMD = re.compile(r"\bexec\.Command(Context)?\s*\(")
OS_FILE_OP = re.compile(
    r"\b(?:os\.(?:Open|OpenFile|Create|ReadFile|WriteFile|Remove|RemoveAll|"
    r"Rename|Stat|Mkdir|MkdirAll|Chmod|Chown|Lstat|Symlink))\s*\("
)
PATH_JOIN = re.compile(r"\b(?:filepath\.(?:Join|Clean|Abs))\s*\(")


def is_safe_identifier(ident):
    """标识符含 hash（哈希存储）或以 Path/File/Dir 等结尾（路径/文件名）时，
    视为非凭据字面量赋值。"""
    low = ident.lower()
    if "hash" in low:
        return True
    return any(low.endswith(suffix) for suffix in SAFE_IDENT_SUFFIXES)


def find_repo_root():
    """从脚本位置向上查找包含 go.mod 的项目根目录。"""
    cur = os.path.dirname(os.path.abspath(__file__))
    for _ in range(6):
        if os.path.exists(os.path.join(cur, "go.mod")):
            return cur
        cur = os.path.dirname(cur)
    sys.exit("❌ Could not locate project root (go.mod not found).")


def is_struct_tag(line, match_start):
    """判断命中位置是否处于 struct tag（如 json:"password"）上下文中。"""
    window = line[max(0, match_start - 24):match_start]
    return any(prefix in window for prefix in TAG_PREFIXES)


def collect_call_window(lines, start, open_paren):
    """从 start 行开始收集括号平衡的调用文本（最多 12 行）。"""
    depth = lines[start].count(open_paren) - lines[start].count(")")
    buf = lines[start]
    idx = start + 1
    while depth > 0 and idx < len(lines) and idx < start + 12:
        buf += lines[idx]
        depth += lines[idx].count(open_paren) - lines[idx].count(")")
        idx += 1
    return buf, idx


def scan():
    root = find_repo_root()
    os.chdir(root)

    issues = {"credential": [], "log": [], "command": [], "path": [], "path_warn": []}

    for scan_dir in SCAN_DIRS:
        if not os.path.isdir(scan_dir):
            continue
        for base, _, files in os.walk(scan_dir):
            for name in sorted(files):
                if not name.endswith(".go") or name.endswith("_test.go"):
                    continue
                path = os.path.join(base, name)
                try:
                    with open(path, "r", encoding="utf-8", errors="ignore") as f:
                        lines = f.readlines()
                except OSError:
                    continue

                for i, line in enumerate(lines):
                    # 1. 硬编码凭据（排除 struct tag / 路径与哈希类标识符）
                    for m in HARDCODED_CRED.finditer(line):
                        if is_struct_tag(line, m.start()):
                            continue
                        ident = m.group(0).split(":", 1)[0].split("=", 1)[0].strip()
                        if is_safe_identifier(ident):
                            continue
                        issues["credential"].append(
                            f"{path}:{i + 1} -> {line.strip()}")

                    # 2. 敏感属性写入日志
                    for m in SENSITIVE_LOG_ATTR.finditer(line):
                        issues["log"].append(
                            f"{path}:{i + 1} -> {line.strip()}")

                    # 3. 命令注入候选：exec.Command 参数中的字符串拼接
                    if EXEC_CMD.search(line):
                        window, _ = collect_call_window(lines, i, "(")
                        if "+" in window and "..." not in window:
                            issues["command"].append(
                                f"{path}:{i + 1} -> {line.strip()}")

                    # 4. 路径穿越候选：os.* 文件操作中的字符串拼接（阻断级）
                    if OS_FILE_OP.search(line):
                        window, _ = collect_call_window(lines, i, "(")
                        if "+" in window:
                            issues["path"].append(
                                f"{path}:{i + 1} -> {line.strip()}")

                    # 5. 路径拼接警告：filepath.Join/Clean/Abs 内字符串拼接（警告级）
                    if PATH_JOIN.search(line):
                        window, _ = collect_call_window(lines, i, "(")
                        if "+" in window:
                            issues["path_warn"].append(
                                f"{path}:{i + 1} -> {line.strip()}")

    print("========================================================================")
    print("  Security Red-Line Static Scan")
    print("========================================================================")

    total = 0
    if issues["credential"]:
        print("\n❌ [硬编码凭据] 禁止以字面量赋值敏感字段（测试 mock 除外）:")
        for item in issues["credential"]:
            print(f"   - {item}")
        total += len(issues["credential"])
    if issues["log"]:
        print("\n❌ [敏感属性日志] 禁止将 password/secret 等属性键写入日志:")
        for item in issues["log"]:
            print(f"   - {item}")
        total += len(issues["log"])
    if issues["command"]:
        print("\n❌ [命令注入候选] exec.Command 参数中出现字符串拼接，需审查是否为用户输入:")
        for item in issues["command"]:
            print(f"   - {item}")
        total += len(issues["command"])
    if issues["path"]:
        print("\n❌ [路径穿越候选] os.* 文件操作中出现字符串拼接，需审查是否经 filepath.Clean 校验:")
        for item in issues["path"]:
            print(f"   - {item}")
        total += len(issues["path"])
    if issues["path_warn"]:
        print("\n⚠️  [路径拼接警告] filepath.Join/Clean/Abs 内出现字符串拼接（Join 会规范化，但仍建议审查）:")
        for item in issues["path_warn"]:
            print(f"   - {item}")

    if total == 0:
        print("\n✅ No security red-line violations detected.")
        if issues["path_warn"]:
            print("⚠️  Warning-level items above do not block, but please review them.")
        sys.exit(0)

    print(f"\n❌ {total} blocking security violation(s) detected. Fix before proceeding.")
    sys.exit(1)


if __name__ == "__main__":
    scan()
