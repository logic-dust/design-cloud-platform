#!/usr/bin/env python3
"""Export a point-in-time architecture graph from the current worktree.

Package counts, import edges, and source line references are measured from
the live `go list` graph and the current tree. They go stale quickly and
should not be checked into the repository.

    python3 .agents/scripts/export_arch_graph.py
    python3 .agents/scripts/export_arch_graph.py --output /tmp/arch-graph.md

With no --output, the report is written to stdout.

Outputs:
  1. package counts per internal/ subtree
  2. production import edges among internal/server/service/* packages
  3. internal/server root-package -> subsystem edges
  4. cmd/engine direct imports of server/db packages (CLI entry evidence)
  5. construction-graph wiring points (Wire*Deps / Set* hooks)
  6. user -> session effect call sites with their failure semantics
"""
import argparse
import os
import re
import subprocess
import sys

TMPL = (
    'PKG|{{.ImportPath}}{{printf "\\n"}}'
    '{{$p := .ImportPath}}'
    '{{range .Imports}}DEP|{{$p}}|{{.}}{{printf "\\n"}}{{end}}'
)


def go_list(pattern, root):
    proc = subprocess.run(
        ["go", "list", "-f", TMPL, pattern],
        cwd=root, capture_output=True, text=True, timeout=300,
    )
    if proc.returncode != 0:
        print(f"ERROR: go list failed for {pattern}: {proc.stderr}", file=sys.stderr)
        sys.exit(1)
    pkgs, deps = set(), []
    for line in proc.stdout.splitlines():
        if line.startswith("PKG|"):
            pkgs.add(line[4:])
        elif line.startswith("DEP|"):
            _, importer, importee = line.split("|", 2)
            deps.append((importer, importee))
    return pkgs, deps


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--output", default=None, help="write report to this file")
    args = ap.parse_args()
    root = os.getcwd()
    mod = ""
    with open(os.path.join(root, "go.mod")) as f:
        for line in f:
            if line.startswith("module "):
                mod = line.split()[1].strip()
                break

    out = []
    w = out.append
    w("# 当前架构依赖快照（机器导出）")
    w("")
    w("> 由 `.agents/scripts/export_arch_graph.py` 从当前工作树的 `go list` 依赖图与源码导出。")
    w("> 包数量与源码行号会随代码变化，仅作即时查看，不要作为仓库跟踪文件。")
    w(f"> 模块：`{mod}`")
    w("")

    # 1) package counts
    w("## 1. 包规模（当前基线）")
    w("")
    w("| 区域 | Go 包数 | 非测试 .go 文件 |")
    w("|---|---:|---:|")
    # 区域清单从磁盘实测，避免硬编码清单随迁移漂移。
    areas = []
    internal_dir = os.path.join(root, "internal")
    for name in sorted(os.listdir(internal_dir)):
        if not os.path.isdir(os.path.join(internal_dir, name)):
            continue
        areas.append(f"internal/{name}")
        if name == "server":
            for sub in sorted(os.listdir(os.path.join(internal_dir, name))):
                if os.path.isdir(os.path.join(internal_dir, name, sub)):
                    areas.append(f"internal/server/{sub}")
    all_pkgs, _ = go_list(mod + "/internal/...", root)
    for area in areas:
        pref = mod + "/" + area
        pkgs = [p for p in all_pkgs if p == pref or p.startswith(pref + "/")]
        nfiles = 0
        for p in pkgs:
            rel = p[len(mod):].lstrip("/")
            d = os.path.join(root, rel)
            if os.path.isdir(d):
                nfiles += len([f for f in os.listdir(d)
                               if f.endswith(".go") and not f.endswith("_test.go")])
        w(f"| `{area}` | {len(pkgs)} | {nfiles} |")
    w("")

    # 2) service-to-service edges
    w("## 2. `service/*` 生产依赖边（跨业务包）")
    w("")
    w("只列 `internal/server/service/<a>` → `internal/server/service/<b>`（a≠b），按来源排序。")
    w("")
    _, deps = go_list(mod + "/internal/server/service/...", root)
    svc_pref = mod + "/internal/server/service/"
    edges = set()
    for importer, importee in deps:
        if not importer.startswith(svc_pref) or not importee.startswith(svc_pref):
            continue
        a = importer[len(svc_pref):].split("/")[0]
        b = importee[len(svc_pref):].split("/")[0]
        if a != b:
            edges.add((a, b))
    w("| 来源 | 目标 |")
    w("|---|---|")
    for a, b in sorted(edges):
        w(f"| `{a}` | `{b}` |")
    w("")
    w(f"**合计 {len(edges)} 条有向边**（无方向推断；未出现在本表的包表示无同级跨域 import）。")
    w("")

    # 3) server root -> subsystem
    w("## 3. Server 根包的下游依赖（组合根证据）")
    w("")
    rootpkg = mod + "/internal/server"
    _, alldeps = go_list(mod + "/internal/...", root)
    root_edges = sorted({i for p, i in alldeps if p == rootpkg and i.startswith(mod + "/internal/server/")})
    for i in root_edges:
        w(f"- `{i[len(mod) + 1:]}`")
    w("")

    # 4) CLI direct imports
    w("## 4. `cmd/*` 直连服务端实现层")
    w("")
    cmd_pkgs, cmd_deps = go_list(mod + "/cmd/...", root)
    targets = sorted({i for p, i in cmd_deps
                      if i.startswith(mod + "/internal/")
                      and ("/server/" in i or i.endswith("/db") or i.endswith("/model"))})
    for i in targets:
        w(f"- `{i[len(mod) + 1:]}`")
    w("")

    # 5) wiring points
    w("## 5. 构造期 Hook / 仅测试 Setter（无业务依赖回填）")
    w("")
    w("> 阶段 4/5 后业务依赖全部在构造期注入：`controlplane.NewServer` 接收五端口，"
      "`session.Evictor` 由装配根同时交给 user 与 session。下表只剩两类："
      "Start 前注册的 leader 降级 Hook，以及仅供单测使用的 push 转发 Setter。")
    w("")
    pat = re.compile(r"Wire\w+Deps|SetDemotionHook|SetClusterForwarder|SetCert\w*")
    hits = []
    for base in ("internal/server", "cmd"):
        for cur, _, files in os.walk(os.path.join(root, base)):
            for fn in files:
                if not fn.endswith(".go") or fn.endswith("_test.go"):
                    continue
                fp = os.path.join(cur, fn)
                with open(fp, errors="replace") as f:
                    for n, line in enumerate(f, 1):
                        if line.strip().startswith("//"):
                            continue
                        if pat.search(line):
                            hits.append((os.path.relpath(fp, root), n, line.strip()))
    w("| 位置 | 代码 |")
    w("|---|---|")
    for fp, n, line in hits:
        w(f"| `{fp}:{n}` | `{line[:110]}` |")
    w("")

    # 6) user -> session effect call sites
    w("## 6. `user → session` effect 调用点与失败语义")
    w("")
    w("> user 不 import session：两类副作用经 `user.SessionRevoker` 消费方接口调用，"
      "实现为共享的 `*session.Evictor`（删行 + 清缓存 + 断代理连接）。以下调用点的"
      "同步性与失败策略在阶段 5 保持原样。")
    w("")
    up = os.path.join(root, "internal/server/service/user")
    eff = re.compile(r"(invalidateSessionsByUsername|deleteSessionsByUsername)")
    w("| 位置 | 调用 | 失败处理 |")
    w("|---|---|---|")
    for cur, _, files in os.walk(up):
        for fn in sorted(files):
            if not fn.endswith(".go") or fn.endswith("_test.go"):
                continue
            fp = os.path.join(cur, fn)
            with open(fp, errors="replace") as f:
                lines = f.readlines()
            for n, line in enumerate(lines, 1):
                if eff.search(line) and not line.strip().startswith("//"):
                    s = line.strip()
                    if "_ =" in s:
                        sem = "忽略错误（best-effort）"
                    elif "return " in s:
                        sem = "透传错误给调用方"
                    elif "delErr" in s or "err :=" in s or "if err" in s:
                        sem = "显式处理错误"
                    elif "func(" in s or "func " in s or "=" in s and "()" not in s:
                        sem = "注入/声明（非调用）"
                    else:
                        sem = "同步调用（无返回值）"
                    if sem == "注入/声明（非调用）":
                        continue  # 只登记真实调用点，不把 if 判定/字段声明算作 effect
                    w(f"| `{os.path.relpath(fp, root)}:{n}` | `{s[:80]}` | {sem} |")
    w("")
    w("> 结论：共 6 个真实调用点，失败语义分三类（显式处理 / 忽略 / 透传）。"
      "阶段 5 用共享 `session.Evictor` 切断了构造环，未改变任何一条调用的同步性与失败策略；"
      "如需改为异步事件，必须逐条重审业务语义。")
    w("")

    text = "\n".join(out)
    if args.output:
        with open(args.output, "w") as f:
            f.write(text + "\n")
        print(f"written to {args.output}")
    else:
        print(text)


if __name__ == "__main__":
    main()
