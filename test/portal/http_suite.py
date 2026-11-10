"""Parse test/portal/*.http and build executable integration cases."""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple, Union

from portal_routes import CONNECTION_OPS, SCHEDULER_JOB_OPS

HTTP_DIR = Path(__file__).resolve().parent

# Dependency order for run_http_tests.py (see test/portal/README.md).
# - Bootstrap: public API → login → 03-bans (clear stale bans) → identities.
# - Per domain: admin create/configure → user read/write → teardown DELETE where applicable.
# - Settings: read-only GET before system-admin PUT.
# - Session/access security (04/05) near end; 22-sse-push last; auth_guard is Python-only at the tail.
HTTP_FILE_ORDER = [
    "01-public-api.http",
    "02-session.http",
    "03-bans-security-admin.http",
    "14-users.http",
    "14-users-system-admin.http",
    "16-settings.http",
    "16-settings-system-admin.http",
    "16-settings-alert-system-admin.http",
    "06-apps-system-admin.http",
    "06-apps.http",
    "07-scheduler.http",
    "07-scheduler-system-admin.http",
    "08-connections-system-admin.http",
    "08-connections.http",
    "09-proxy.http",
    "10-files.http",
    "10-files-security-admin.http",
    "12-nodes-system-admin.http",
    "13-licenses-system-admin.http",
    "13-licenses.http",
    "17-audit.http",
    "18-overview.http",
    "19-status-storage.http",
    "21-tickets-system-admin.http",
    "21-tickets.http",
    "04-sessions-security-admin.http",
    "05-access-security-admin.http",
    "22-sse-push.http",
]

METHOD_LINE = re.compile(r"^(GET|POST|PUT|DELETE|PATCH)\s+(\S+)", re.MULTILINE)
EXPECT_LINE = re.compile(r"^#\s*@expect\s+([\d,\s]+)", re.MULTILINE | re.IGNORECASE)


@dataclass
class ParsedHttpRequest:
    source_file: str
    title: str
    method: str
    path_template: str
    headers: Dict[str, str] = field(default_factory=dict)
    body: Optional[Dict[str, Any]] = None
    raw_body: Optional[str] = None
    expect: Tuple[int, ...] = (200,)
    manual: bool = False
    probe: bool = False
    expect_tagged: bool = False  # title has explicit # @expect (strict honors 404/409 when listed)
    token_mode: str = "current"  # current | none | literal


def _parse_expect(block: str, method: str) -> Tuple[int, ...]:
    method_match = METHOD_LINE.search(block)
    prefix = block[: method_match.start()] if method_match else block
    m = EXPECT_LINE.search(prefix)
    if m:
        return tuple(int(x.strip()) for x in m.group(1).split(",") if x.strip())
    if method == "GET":
        return (200,)
    if method == "POST":
        return (200, 201, 202, 204, 400, 403, 404, 422)
    if method in ("PUT", "PATCH"):
        return (200, 204, 400, 403, 404, 422)
    if method == "DELETE":
        return (200, 204, 400, 403, 404)
    return (200,)


def _extract_json_block(rest: str) -> Optional[str]:
    """Raw JSON object text after headers (stops at JetBrains `> {%` script)."""
    lines = rest.splitlines()
    body_lines: List[str] = []
    in_json = False
    depth = 0
    for line in lines:
        if line.strip().startswith("> {%"):
            break
        if line.strip().startswith("#"):
            continue
        if not in_json:
            if line.strip().startswith("{"):
                in_json = True
            else:
                continue
        body_lines.append(line)
        depth += line.count("{") - line.count("}")
        if in_json and depth <= 0:
            break
    if not body_lines:
        return None
    return "\n".join(body_lines)


def _parse_body(block: str, start_idx: int) -> Optional[Dict[str, Any]]:
    raw = _extract_json_block(block[start_idx:])
    if raw is None:
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return None


def _parse_raw_body(rest: str, headers: Dict[str, str], json_body: Optional[Dict[str, Any]]) -> Optional[str]:
    """Non-JSON bodies (e.g. text/plain upload) after headers until script or next block."""
    if json_body is not None:
        return None
    ct = ""
    for key, val in headers.items():
        if key.lower() == "content-type":
            ct = val.lower()
    if not ct or "json" in ct:
        return None
    lines = rest.splitlines()
    past_headers = False
    body_lines: List[str] = []
    for line in lines:
        if line.strip().startswith("> {%"):
            break
        if METHOD_LINE.match(line):
            continue
        if not past_headers:
            if line.strip() == "":
                past_headers = True
            continue
        if line.strip().startswith("#"):
            continue
        body_lines.append(line)
    if not body_lines:
        return None
    return "\n".join(body_lines)


def _tagged_titles_from_file(content: str, tag: str) -> set[str]:
    """Titles for blocks whose preceding line is `# @tag` (JetBrains-style placement)."""
    titles: set[str] = set()
    pat = re.compile(rf"^\s*#\s*@{tag}\b", re.IGNORECASE)
    lines = content.splitlines()
    for i, line in enumerate(lines):
        if not pat.match(line):
            continue
        for j in range(i + 1, len(lines)):
            s = lines[j].strip()
            if not s:
                continue
            if s.startswith("###"):
                titles.add(s.lstrip("#").strip())
                break
            if s.startswith("#"):
                continue
            break
    return titles


def _manual_titles_from_file(content: str) -> set[str]:
    return _tagged_titles_from_file(content, "manual")


def _expect_overrides_from_file(content: str) -> Dict[str, Tuple[int, ...]]:
    """Map request title → status codes when `# @expect` is immediately above `###`."""
    overrides: Dict[str, Tuple[int, ...]] = {}
    lines = content.splitlines()
    for i, line in enumerate(lines):
        m = EXPECT_LINE.search(line)
        if not m:
            continue
        expect = tuple(int(x.strip()) for x in m.group(1).split(",") if x.strip())
        for j in range(i + 1, len(lines)):
            s = lines[j].strip()
            if not s:
                continue
            if s.startswith("###"):
                overrides[s.lstrip("#").strip()] = expect
                break
            if s.startswith("#"):
                continue
            break
    return overrides


def parse_http_file(path: Path) -> List[ParsedHttpRequest]:
    content = path.read_text(encoding="utf-8")
    manual_titles = _manual_titles_from_file(content)
    probe_titles = _tagged_titles_from_file(content, "probe")
    expect_overrides = _expect_overrides_from_file(content)
    blocks = re.split(r"(?m)^###\s+", content)
    out: List[ParsedHttpRequest] = []
    for block in blocks[1:]:
        if not block.strip():
            continue
        title_line, rest = block.split("\n", 1)
        title = title_line.strip()
        m = METHOD_LINE.search(rest)
        header_prefix = rest[: m.start()] if m else rest
        manual = title in manual_titles or bool(
            re.search(r"^#\s*@manual\b", header_prefix, re.MULTILINE | re.IGNORECASE)
        )
        probe = title in probe_titles or bool(
            re.search(r"^#\s*@probe\b", header_prefix, re.MULTILINE | re.IGNORECASE)
        )
        if not m:
            continue
        method = m.group(1).upper()
        path_template = m.group(2).strip()
        if not path_template.startswith("{{addr}}") and not path_template.startswith("/api/"):
            continue
        headers: Dict[str, str] = {}
        for hline in rest.splitlines():
            if ":" not in hline or hline.strip().startswith("#"):
                continue
            if hline.strip().startswith("{") or hline.strip().startswith(">"):
                break
            key, val = hline.split(":", 1)
            key = key.strip()
            if key.lower() in ("content-type", "authorization", "accept"):
                headers[key] = val.strip()
        token_mode = "current"
        auth = headers.get("Authorization", "")
        if not auth:
            token_mode = "none"
        elif "Bearer {{accessToken}}" not in auth and "Bearer" in auth:
            token_mode = "literal"
        body = _parse_body(rest, 0)
        raw_body = _parse_raw_body(rest, headers, body)
        if body is None and raw_body is None:
            template = _extract_json_block(rest)
            if template and "{{" in template:
                raw_body = template
        expect = expect_overrides.get(title) or _parse_expect(block, method)
        out.append(
            ParsedHttpRequest(
                source_file=path.name,
                title=title,
                method=method,
                path_template=path_template,
                headers=headers,
                body=body,
                raw_body=raw_body,
                expect=expect,
                manual=manual,
                probe=probe,
                expect_tagged=title in expect_overrides,
                token_mode=token_mode,
            )
        )
    return out


def load_all_http_requests() -> List[ParsedHttpRequest]:
    requests: List[ParsedHttpRequest] = []
    seen_files = set()
    for name in HTTP_FILE_ORDER:
        path = HTTP_DIR / name
        if not path.is_file():
            continue
        seen_files.add(name)
        requests.extend(parse_http_file(path))
    for path in sorted(HTTP_DIR.glob("*.http")):
        if path.name in seen_files:
            continue
        requests.extend(parse_http_file(path))
    return requests


def substitute_path(path_template: str, vars: Dict[str, str]) -> Optional[str]:
    path = path_template
    if path.startswith("{{addr}}"):
        path = re.sub(r"^\{\{addr\}\}", "", path)
    unresolved = re.findall(r"\{\{(\w+)\}\}", path)
    for key in unresolved:
        if key not in vars or vars[key] in ("", "__run_login_first__"):
            return None
        path = path.replace("{{" + key + "}}", str(vars[key]))
    if "{{" in path:
        return None
    return path


def substitute_body(body: Optional[Dict[str, Any]], vars: Dict[str, str]) -> Optional[Dict[str, Any]]:
    if body is None:
        return None
    raw = json.dumps(body)
    for key, val in vars.items():
        raw = raw.replace("{{" + key + "}}", str(val))
    if "{{" in raw:
        return None
    return json.loads(raw)


_TICKET_LAB_PROJECT_NAME = "dc-portal-http-suite"
_FIXED_TEST_VNC_RESOURCE_NAME = "fixed-test-vnc-resource"
_PUBLIC_WINDOWS_RDP_RESOURCE_NAME = "public-windows-rdp"


def _capture_ticket_lab_project(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        if row.get("name") == _TICKET_LAB_PROJECT_NAME:
            pid = row.get("id")
            if pid is not None:
                return {"testProjectId": str(pid)}
    return {}


def _list_rows(d: Dict[str, Any]) -> List[Dict[str, Any]]:
    data = d.get("data")
    if isinstance(data, list):
        return [r for r in data if isinstance(r, dict)]
    if isinstance(data, dict):
        items = data.get("items")
        if isinstance(items, list):
            return [r for r in items if isinstance(r, dict)]
        jobs = data.get("jobs") or data.get("Jobs")
        if isinstance(jobs, list):
            return [r for r in jobs if isinstance(r, dict)]
    return []


def _capture_app_list(d: Dict[str, Any]) -> Dict[str, str]:
    preferred = ("test_app", "updated_app1")
    for name in preferred:
        for row in _list_rows(d):
            if row.get("name") == name and row.get("id") is not None:
                return {"testAppId": str(row["id"])}
    for row in _list_rows(d):
        if row.get("id") is not None:
            return {"testAppId": str(row["id"])}
    return {}


def _capture_license_list(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        name = row.get("name")
        if name in ("test-lic-service", "updated-lic-service") and row.get("id") is not None:
            return {"testLicenseId": str(row["id"])}
    return {}


def _capture_scheduler_job(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        job_id = (
            row.get("JOBID")
            or row.get("job_id")
            or row.get("jobId")
            or row.get("id")
        )
        if job_id is not None and str(job_id) not in ("", "0"):
            return {"testJobId": str(job_id)}
    return {}


def _capture_execution_list(d: Dict[str, Any]) -> Dict[str, str]:
    data = d.get("data")
    if isinstance(data, dict):
        items = data.get("items")
        if isinstance(items, list) and items:
            row = items[0]
            if isinstance(row, dict) and row.get("executionId"):
                return {"testExecutionId": str(row["executionId"])}
    return {}


def _capture_download_request_list(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        rid = row.get("downloadRequestId") or row.get("id")
        if rid is not None:
            return {"testDownloadRequestId": str(rid)}
    return {}


def _capture_admin_connection_resource(d: Dict[str, Any]) -> Dict[str, str]:
    """Admin POST 创建规格：按 name 写入 VNC/RDP 夹具 id，避免 RDP 创建覆盖 VNC 的 testResourceID。"""
    row = d.get("data")
    if not isinstance(row, dict):
        return {}
    rid = row.get("id")
    if rid is None:
        return {}
    name = row.get("name")
    sid = str(rid)
    if name == _FIXED_TEST_VNC_RESOURCE_NAME:
        return {"testResourceID": sid, "userResourceID": sid}
    if name == _PUBLIC_WINDOWS_RDP_RESOURCE_NAME:
        return {"testRDPResourceID": sid}
    return {}


def _capture_connection_resource_list(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        if row.get("name") == _FIXED_TEST_VNC_RESOURCE_NAME and row.get("id") is not None:
            return {
                "testResourceID": str(row["id"]),
                "userResourceID": str(row["id"]),
            }
    return {}


def _capture_user_connection_list(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        cid = row.get("id")
        if cid is None:
            continue
        proto = str(row.get("protocol") or "").upper()
        out = {"testConnectionId": str(cid)}
        if proto == "VNC":
            out["testVNCConnectionId"] = str(cid)
        return out
    return {}


def _capture_ticket_list(d: Dict[str, Any]) -> Dict[str, str]:
    for row in _list_rows(d):
        if row.get("id") is not None:
            return {"testTicketId": str(row["id"]), "testTicketVersion": str(row.get("version") or "")}
    return {}


_TICKET_VERSIONED_PATH = re.compile(r"^/api/tickets/\d+(/move)?$")
_TICKET_COMMENTS_PATH = re.compile(r"^/api/tickets/\d+/comments$")


CAPTURE_RULES: List[Tuple[str, str, Callable[[Dict[str, Any]], Dict[str, str]]]] = [
    (
        "POST",
        "/api/sessions",
        lambda d: {"accessToken": str((d.get("data") or {}).get("token") or "")},
    ),
    (
        "POST",
        "/api/admin/system/apps",
        lambda d: {"testAppId": str((d.get("data") or {}).get("id") or "")},
    ),
    (
        "POST",
        "/api/tickets",
        lambda d: {
            "testTicketId": str((d.get("data") or {}).get("id") or ""),
            "testTicketVersion": str((d.get("data") or {}).get("version") or ""),
        },
    ),
    (
        "POST",
        "/api/admin/system/tickets/projects",
        lambda d: {"testProjectId": str((d.get("data") or {}).get("id") or "")},
    ),
    (
        "GET",
        "/api/admin/system/tickets/projects",
        lambda d: _capture_ticket_lab_project(d),
    ),
    (
        "POST",
        "/api/admin/system/licenses",
        lambda d: {"testLicenseId": str((d.get("data") or {}).get("id") or "")},
    ),
    ("POST", "/api/admin/system/connections/resources", _capture_admin_connection_resource),
    (
        "GET",
        "/api/admin/system/connections",
        lambda d: {
            "adminConnectionId": str(
                (d.get("data") or [{}])[0].get("id") or ""
            )
            if isinstance(d.get("data"), list) and d.get("data")
            else ""
        },
    ),
    ("GET", "/api/admin/system/apps", _capture_app_list),
    ("GET", "/api/admin/system/licenses", _capture_license_list),
    ("GET", "/api/licenses", _capture_license_list),
    ("GET", "/api/scheduler/jobs/me", _capture_scheduler_job),
    ("GET", "/api/scheduler/jobs", _capture_scheduler_job),
    ("GET", "/api/admin/system/scheduler/jobs", _capture_scheduler_job),
    ("GET", "/api/admin/audit/executions", _capture_execution_list),
    ("GET", "/api/files/download-requests", _capture_download_request_list),
    (
        "GET",
        "/api/admin/security/files/download-requests",
        _capture_download_request_list,
    ),
    ("GET", "/api/connections/resources", _capture_connection_resource_list),
    ("GET", "/api/admin/system/connections/resources", _capture_connection_resource_list),
    ("GET", "/api/connections", _capture_user_connection_list),
    ("GET", "/api/tickets", _capture_ticket_list),
]


def apply_capture_rules(method: str, path: str, data: Dict[str, Any], vars: Dict[str, str]) -> None:
    base = path.split("?")[0]
    for m, p, fn in CAPTURE_RULES:
        if m == method and base == p:
            for k, v in fn(data).items():
                if v:
                    vars[k] = v
    payload = data.get("data") if isinstance(data.get("data"), dict) else {}
    if method in ("GET", "PATCH", "POST") and _TICKET_VERSIONED_PATH.match(base) and payload.get("version"):
        vars["testTicketVersion"] = str(payload["version"])
    if method == "POST" and _TICKET_COMMENTS_PATH.match(base) and payload.get("id"):
        vars["testCommentId"] = str(payload["id"])
# Placeholder IDs in http-client.env.json must not override values captured during the run.
_ENV_SKIP_OVERRIDE = frozenset(
    {
        "testAppId",
        "testJobId",
        "testTicketId",
        "testTicketVersion",
        "testCommentId",
        "testProjectId",
        "testLicenseId",
        "testLicenseIdOmitEnable",
        "testConnectionId",
        "testVNCConnectionId",
        "testResourceID",
        "testRDPResourceID",
        "adminConnectionId",
        "userResourceID",
        "testDownloadRequestId",
        "testExecutionId",
    }
)


def default_vars_from_env(env: Dict[str, Any]) -> Dict[str, str]:
    defaults = {
        "testAppId": "1",
        "testJobId": "0",
        "testTicketId": "1",
        "testTicketVersion": "1",
        "testCommentId": "1",
        "testProjectId": "1",
        "testLicenseId": "1",
        "testLicenseIdOmitEnable": "1",
        "testConnectionId": "1",
        "testVNCConnectionId": "1",
        "testResourceID": "1",
        "testRDPResourceID": "1",
        "testDownloadRequestId": "1",
        "testExecutionId": "1",
        "testTrashName": "new_dir",
        "testFileLocation": "home",
        "testDesktopFilename": "latest",
        "testNodeToken": "token",
        "adminConnectionId": "1",
        "userResourceID": "1",
        "accessToken": "",
    }
    out = {k: str(v) for k, v in defaults.items()}
    for k, v in env.items():
        if k in _ENV_SKIP_OVERRIDE:
            continue
        if isinstance(v, (str, int, float, bool)):
            out[k] = str(v)
    if out.get("accessToken") == "__run_login_first__":
        out["accessToken"] = ""
    return out


def build_http_execution_plan(
    env: Dict[str, Any],
) -> Tuple[List[ParsedHttpRequest], Dict[str, str]]:
    vars = default_vars_from_env(env)
    plan: List[ParsedHttpRequest] = []
    for req in load_all_http_requests():
        if req.manual:
            continue
        plan.append(req)
    return plan, vars


def resolve_request(
    req: ParsedHttpRequest, vars: Dict[str, str]
) -> Optional[
    Tuple[str, str, Optional[Dict[str, Any]], Optional[str], Optional[str], Tuple[int, ...], str]
]:
    path = substitute_path(req.path_template, vars)
    if path is None:
        return None
    body = substitute_body(req.body, vars)
    if req.body is not None and body is None:
        return None
    content_type = None
    for key, val in req.headers.items():
        if key.lower() == "content-type":
            content_type = val
    raw_body: Optional[str] = req.raw_body
    if raw_body is not None:
        substituted = raw_body
        for key, val in vars.items():
            substituted = substituted.replace("{{" + key + "}}", str(val))
        if "{{" in substituted:
            return None
        raw_body = substituted
    return req.method, path, body, raw_body, content_type, req.expect, req.token_mode
