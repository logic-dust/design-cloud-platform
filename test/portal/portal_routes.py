"""Portal API route inventory shared by verify_http_tests.py and run_http_tests.py."""

from __future__ import annotations

import os
import re
from pathlib import Path
from typing import Iterable, List, Set, Tuple

Route = Tuple[str, str]  # (METHOD, path template)

SETTINGS_DOMAINS = (
    "platform",
    "auth",
    "connection",
    "file",
    "overview",
    "storage",
    "scheduler",
    "license",
    "agent",
    "alert",
    "desktop",
    "cert",
)

CONNECTION_OPS = ("start", "stop", "restart")
SCHEDULER_JOB_OPS = ("kill", "suspend", "resume", "top", "bottom", "switch")
FILE_LOCATION_NAMES = ("home", "public", "shared-data")
AUDIT_LOG_NAMES = ("audit", "security")
ROLE_NAMES = ("superadmin", "systemAdmin", "securityAdmin")


ROUTE_INVENTORY_FILE = Path(__file__).resolve().parent / "portal_routes.txt"


def parse_route_templates() -> Set[Route]:
    """Return the registered Portal routes.

    权威来源是 test/portal/portal_routes.txt：由 Go 测试 TestPortalRouteInventory 依据
    InitRouter 的实际注册结果生成并校验（路由变更时该测试失败，提示重新生成）。
    """
    routes: Set[Route] = set()
    for line in ROUTE_INVENTORY_FILE.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        method, path = line.split(None, 1)
        routes.add((method.upper(), path))
    return routes


def _expand_segment(seg: str, env: dict) -> List[str]:
    if not seg.startswith(":"):
        return [seg]
    name = seg[1:]
    if name == "domain" and "settings" in env.get("_path", ""):
        return list(SETTINGS_DOMAINS)
    if name == "op":
        if "scheduler/jobs" in env.get("_path", ""):
            return list(SCHEDULER_JOB_OPS)
        if "connections" in env.get("_path", ""):
            return list(CONNECTION_OPS)
        return ["start"]
    if name == "name" and "audit/logs" in env.get("_path", ""):
        return ["audit", "security"]
    if name == "name" and "plugins" in env.get("_path", ""):
        return ["turbovnc"]
    if name == "role":
        return ["superadmin", "systemAdmin"]
    if name == "group":
        return [env.get("testGroup", "cad")]
    if name == "user":
        return [env.get("username", "hpcadmin"), env.get("normalUser", "test1")]
    if name == "username":
        return [env.get("normalUser", "test1")]
    if name == "key":
        return ["127.0.0.1"]
    if name == "id":
        path = env.get("_path", "")
        if "/nodes/" in path:
            return [env.get("testNode", "cent7")]
        if "/apps/" in path:
            return [env.get("testAppId", "1")]
        if "/tickets/projects/" in path:
            return [env.get("testProjectId", "1")]
        if "/tickets/" in path:
            return [env.get("testTicketId", "1")]
        if "/licenses/" in path:
            return [env.get("testLicenseId", "1")]
        if "/connections/resources/" in path:
            return [env.get("testRDPResourceID", "1")]
        if "/connections/" in path:
            return [env.get("testConnectionId", "1"), env.get("testVNCConnectionId", "1")]
        if "/files/download-requests/" in path:
            return [env.get("testDownloadRequestId", "1")]
        if "/storage/" in path:
            return [env.get("testStorageId", "default")]
        return ["1"]
    if name == "executionId":
        return [env.get("testExecutionId", "1")]
    if name == "filename":
        return [env.get("testDesktopFilename", "latest")]
    if name == "trashName":
        return [env.get("testTrashName", "new_dir")]
    return [env.get(name, "1")]


def expand_route(method: str, template: str, env: dict) -> List[Route]:
    """Expand parameterized templates into concrete (method, path) instances."""
    if "*" in template:
        # Wildcard file paths: one concrete instance from env or skip expansion here
        loc = env.get("testFileLocation", "home")
        concrete = template.replace("/*", f"/{loc}/probe")
        return [(method, concrete)]
    parts = [p for p in template.split("/") if p]
    env = {**env, "_path": template}
    options: List[List[str]] = []
    for part in parts:
        if part.startswith(":"):
            options.append(_expand_segment(part, env))
        else:
            options.append([part])
    results: List[Route] = []
    stack = [("", 0)]

    def walk(prefix: str, depth: int) -> None:
        if depth == len(options):
            path = prefix or "/"
            if not path.startswith("/api/"):
                path = "/" + path.lstrip("/")
            results.append((method, path))
            return
        for choice in options[depth]:
            seg = choice.strip("/")
            nxt = f"{prefix}/{seg}" if prefix else f"/{seg}"
            walk(nxt, depth + 1)

    walk("", 0)
    # Deduplicate while preserving order
    seen: Set[str] = set()
    out: List[Route] = []
    for m, p in results:
        key = f"{m} {p}"
        if key in seen:
            continue
        seen.add(key)
        if "__skip__" in p:
            continue
        out.append((m, p))
    return out


def path_template_from_http(http_path: str) -> str:
    """Map {{addr}}/api/foo/{{var}} to /api/foo/:id style for matching."""
    p = re.sub(r"^\{\{addr\}\}", "", http_path).split("?")[0]
    parts = [seg for seg in p.split("/") if seg]
    out: List[str] = []
    for i, part in enumerate(parts):
        if part.startswith("{{") and part.endswith("}}"):
            name = part[2:-2]
            mapping = {
                "testAppId": ":id",
                "testTicketId": ":id",
                "testProjectId": ":id",
                "testLicenseId": ":id",
                "testLicenseIdOmitEnable": ":id",
                "testConnectionId": ":id",
                "testVNCConnectionId": ":id",
                "adminConnectionId": ":id",
                "testResourceID": ":id",
                "testRDPResourceID": ":id",
                "testSSHResourceID": ":id",
                "testNode": ":id",
                "testDownloadRequestId": ":id",
                "testExecutionId": ":executionId",
                "testCommentId": ":commentId",
                "username": ":username",
                "normalUser": ":username",
                "testGroup": ":group",
                "testTrashName": ":trashName",
                "testDesktopFilename": ":filename",
                "testPluginName": ":name",
            }
            out.append(mapping.get(name, ":id"))
        elif i > 0 and parts[i - 1] == "plugins":
            out.append(":name")
        elif part in CONNECTION_OPS or part in SCHEDULER_JOB_OPS:
            out.append(":op")
        elif (
            i >= 2
            and parts[i - 1] == "settings"
            and parts[i - 2] == "system"
            and part in SETTINGS_DOMAINS
        ):
            out.append(":domain")
        elif i > 0 and parts[i - 1] == "locations" and part in FILE_LOCATION_NAMES:
            out.append(":name")
        elif i > 0 and parts[i - 1] == "logs" and part in AUDIT_LOG_NAMES:
            out.append(":name")
        elif i > 0 and parts[i - 1] == "roles" and part in ROLE_NAMES:
            out.append(":role")
        elif i > 0 and parts[i - 1] == "bans" and part not in ("whitelist", "allowlist"):
            out.append(":key")
        elif (
            i >= 2
            and parts[i - 2] == "trash"
            and parts[i - 1] == "items"
            and part not in ("restore", "batch-delete")
        ):
            out.append(":trashName")
        elif i > 0 and parts[i - 1] in ("sessions", "connections", "jobs") and part.isdigit():
            out.append(":id")
        elif i >= 2 and parts[i - 2] == "system" and parts[i - 1] == "storage" and part not in ("refresh",):
            out.append(":id")
        elif i > 0 and parts[i - 1] == "download" and part.endswith((".nupkg", ".exe", ".dmg", ".zip")):
            out.append(":filename")
        else:
            out.append(part)
    return "/" + "/".join(out)


def _normalize_param_templates(path: str) -> str:
    return path.replace(":username", ":user")


def http_covers_route(method: str, route_template: str, http_method: str, http_path: str) -> bool:
    if method != http_method.upper():
        return False
    tmpl = _normalize_param_templates(path_template_from_http(http_path))
    rt = _normalize_param_templates(route_template if route_template.startswith("/") else "/" + route_template)
    if tmpl == rt or tmpl.rstrip("/") == rt.rstrip("/"):
        return True
    # Wildcard file paths: GET .../contents/* vs concrete location in .http
    if "/*" in rt:
        prefix = rt.split("/*", 1)[0]
        if tmpl == prefix or tmpl.startswith(prefix + "/"):
            return True
    return False
