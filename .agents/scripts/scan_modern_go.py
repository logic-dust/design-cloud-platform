#!/usr/bin/env python3
"""
scan_modern_go.py - Modern Go Feature Audit Scanner
扫描 internal/ 下非测试 Go 文件，识别可迁移到 Go 1.21+ 现代范式（cmp.Or, slices,
maps, clear, min/max, strings.Lines, errors.AsType）的候选点。

检测模式:
  🔹 cmp.Or candidate   : if x == "" / == 0 / == nil 后紧跟 x = fallback 赋值
  🔹 maps.Copy candidate: for k, v := range src { dst[k] = v }
  🔹 clear candidate    : for k := range m { delete(m, k) }
  🔹 slices.Contains    : 手动循环 + 相等判断 + return true
  🔹 slices.Index       : 手动循环 + 相等判断 + return i
  🔹 slices.Sort        : sort.Slice( 手动比较器
  🔹 min/max candidate  : if x < y { x = y } / if x > y { x = y } 边界裁剪
  🔹 strings.Lines      : strings.Split(s, "\\n") 逐行迭代
  🔹 maps.Keys/Values   : for k := range m { keys = append(keys, k) }
  🔹 errors.AsType      : errors.As(err, &target) 可换 AsType[T]

本脚本为纯扫描器（exit 0），结果需人工确认后重构。

用法:
  python3 .agents/scripts/scan_modern_go.py            # 全量扫描
  python3 .agents/scripts/scan_modern_go.py -v         # 含文件行明细
"""

import os
import re
import sys

WINDOW = 3  # 模式窗口：主模式命中后，在后续 N 行内查找次模式


def get_go_version(repo_root):
    gomod_path = os.path.join(repo_root, "go.mod")
    if os.path.exists(gomod_path):
        with open(gomod_path, "r", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line.startswith("go "):
                    return line.split()[1]
    return "1.21"


def scan_window(lines, i, primary, sequence):
    """主模式在第 i 行命中后，在后续行中按顺序匹配次模式序列（支持变量回引）。"""
    m = primary.search(lines[i])
    if not m:
        return None
    groups = {k: v for k, v in m.groupdict().items() if v}
    pos = i + 1
    for rx in sequence:
        matched = False
        for j in range(pos, min(pos + WINDOW, len(lines))):
            s = rx.search(lines[j])
            if not s:
                continue
            if groups and any(
                s.groupdict().get(k) is not None and s.groupdict()[k] != v
                for k, v in groups.items() if k in s.groupdict()
            ):
                continue
            pos = j + 1
            matched = True
            break
        if not matched:
            return None
    return True


def find_repo_root():
    """从脚本位置向上查找包含 go.mod 的项目根目录。"""
    cur = os.path.dirname(os.path.abspath(__file__))
    for _ in range(6):
        if os.path.exists(os.path.join(cur, "go.mod")):
            return cur
        cur = os.path.dirname(cur)
    sys.exit("❌ Could not locate project root (go.mod not found).")


def main():
    verbose = "-v" in sys.argv
    repo_root = find_repo_root()
    internal_dir = os.path.join(repo_root, "internal")
    go_ver = get_go_version(repo_root)

    # 预编译模式：value = (主模式, 次模式序列) —— 次模式需在后续 WINDOW 行内按顺序命中
    P = {
        "cmp.Or": (
            re.compile(r'if\s+(?P<v>[A-Za-z_]\w*)\s*==\s*(""|0|nil)\s*\{\s*$'),
            # 右侧须为字面量/常量（cmp.Or 不适用于昂贵函数调用）
            [re.compile(r'^\s*(?P<v>[A-Za-z_]\w*)\s*=\s*("[^"]*"|\d+|\w+)\s*$')],
        ),
        "maps.Copy": (
            re.compile(r'for\s+(?P<k>[A-Za-z_]\w*),\s*(?P<v>[A-Za-z_]\w*)\s*:=\s*range\s+\w+\s*\{\s*$'),
            [re.compile(r'^\s*\w+\[(?P<k>[A-Za-z_]\w*)\]\s*=\s*(?P<v>[A-Za-z_]\w*)\s*$')],
        ),
        "clear": (
            re.compile(r'for\s+\w+\s*:=\s*range\s+\w+\s*\{\s*$'),
            [re.compile(r'^\s*delete\(\w+,\s*\w+\)\s*$')],
        ),
        "slices.Contains": (
            re.compile(r'for\s+_,\s*(?P<v>[A-Za-z_]\w*)\s*:=\s*range\s+\w+\s*\{\s*$'),
            [
                re.compile(r'^\s*if\s+(?P<v>[A-Za-z_]\w*)\s*==\s*'),
                re.compile(r'^\s*return\s+true\s*$'),
            ],
        ),
        "slices.Index": (
            re.compile(r'for\s+(?P<i>[A-Za-z_]\w*),\s*(?P<v>[A-Za-z_]\w*)\s*:=\s*range\s+\w+\s*\{\s*$'),
            [
                re.compile(r'^\s*if\s+(?P<v>[A-Za-z_]\w*)\s*==\s*'),
                re.compile(r'^\s*return\s+(?P<i>[A-Za-z_]\w*)\s*$'),
            ],
        ),
        "slices.Sort": (
            re.compile(r'sort\.Slice\('),
            [],
        ),
        "min/max": (
            re.compile(r'if\s+(?P<a>[A-Za-z_]\w*)\s*[<>]\s*(?P<b>[A-Za-z_]\w*)\s*\{\s*$'),
            [re.compile(r'^\s*(?P<a>[A-Za-z_]\w*)\s*=\s*(?P<b>[A-Za-z_]\w*)\s*$')],
        ),
        "strings.Lines": (
            re.compile(r'strings\.Split\(\w+,\s*"\\n"\)'),
            [],
        ),
        "maps.Keys/Values": (
            re.compile(r'for\s+(?P<k>[A-Za-z_]\w*)\s*:=\s*range\s+(?P<m>[A-Za-z_]\w*)\s*\{\s*$'),
            [re.compile(r'^\s*\w+\s*=\s*append\([^,]+,\s*(?P<k>[A-Za-z_]\w*)\)\s*$')],
        ),
        "errors.AsType": (
            re.compile(r'errors\.As\([^,]+,\s*&(?P<t>[A-Za-z_]\w*)\)'),
            [],
        ),
    }

    found = {}
    for root, dirs, files in os.walk(internal_dir):
        dirs[:] = [d for d in dirs if not d.startswith("__")]
        for f in sorted(files):
            if not f.endswith(".go") or f.endswith("_test.go"):
                continue
            fp = os.path.join(root, f)
            rel = os.path.relpath(fp, repo_root)
            with open(fp, "r", encoding="utf-8", errors="ignore") as fh:
                lines = fh.readlines()

            for i, line in enumerate(lines):
                for name, (primary, sequence) in P.items():
                    if scan_window(lines, i, primary, sequence):
                        found.setdefault(name, []).append(
                            f"{rel}:{i + 1}: {line.strip()}"
                        )

    print("========================================================================")
    print(f"  Modern Go Feature Audit (Detected SDK: Go {go_ver}+)")
    print("========================================================================")

    total = 0
    for name in sorted(P.keys()):
        items = found.get(name, [])
        if items:
            total += len(items)
            print(f"\n🔹 [{name}] {len(items)} candidate(s):")
            if verbose:
                for it in items:
                    print(f"   {it}")
        else:
            print(f"✅ [{name}] none")

    print("\n-----------------------------------------------------------------------")
    print(f"Total candidates: {total} (review manually before refactoring)")
    print("Hint: run with -v to show file:line details for each candidate.")
    sys.exit(0)


if __name__ == "__main__":
    main()
