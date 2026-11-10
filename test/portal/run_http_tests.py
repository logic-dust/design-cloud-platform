#!/usr/bin/env python3
"""
Design Cloud Engine - Portal HTTP API Test Runner
Directly executes end-to-end integration tests using definitions and configurations
from test/portal/http-client.env.json and corresponding *.http modules.
"""

import argparse
import json
import os
import ssl
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple, Union

SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent.parent
ENV_FILE = SCRIPT_DIR / "http-client.env.json"
DEFAULT_ENV = "dev-ldap-hpcadmin"
REMOTE_BUILD_SCRIPT = REPO_ROOT / ".vscode" / "remote.sh"


def encode_request_path(path: str) -> str:
    """Percent-encode path and query so non-ASCII (e.g. q=登录) is valid in urllib."""
    if "?" not in path:
        return urllib.parse.quote(path, safe="/")
    base, query = path.split("?", 1)
    pairs = urllib.parse.parse_qsl(query, keep_blank_values=True)
    encoded_query = urllib.parse.urlencode(pairs, encoding="utf-8")
    return f"{urllib.parse.quote(base, safe='/')}?{encoded_query}"

sys.path.insert(0, str(SCRIPT_DIR))
from http_suite import (  # noqa: E402
    HTTP_FILE_ORDER,
    _list_rows,
    apply_capture_rules,
    build_http_execution_plan,
    load_all_http_requests,
    resolve_request,
)

class Colors:
    GREEN = "\033[32m"
    RED = "\033[31m"
    YELLOW = "\033[33m"
    BLUE = "\033[34m"
    CYAN = "\033[36m"
    BOLD = "\033[1m"
    RESET = "\033[0m"

    @classmethod
    def disable(cls):
        cls.GREEN = ""
        cls.RED = ""
        cls.YELLOW = ""
        cls.BLUE = ""
        cls.CYAN = ""
        cls.BOLD = ""
        cls.RESET = ""

class TestCase:
    def __init__(
        self,
        domain: str,
        name: str,
        method: str,
        path: str,
        body: Optional[Dict[str, Any]] = None,
        raw_body: Optional[str] = None,
        content_type: Optional[str] = None,
        token: Optional[Union[str, Callable[[], Optional[str]]]] = "__CURRENT__",
        expected_status: Union[int, List[int], Tuple[int, ...]] = 200,
        validator: Optional[Callable[[Dict[str, Any]], Tuple[bool, str]]] = None,
        skip_if: Optional[Callable[[], bool]] = None,
        path_factory: Optional[Callable[[], str]] = None,
        body_factory: Optional[Callable[[], Dict[str, Any]]] = None,
        probe: bool = False,
        strict_expected: Optional[Union[int, List[int], Tuple[int, ...]]] = None,
        http_expect_tagged: bool = False,
    ):
        self.domain = domain
        self.name = name
        self.method = method
        self.path = path
        self.body = body
        self.raw_body = raw_body
        self.content_type = content_type
        self.token = token
        self.expected_status = expected_status
        self.validator = validator
        self.skip_if = skip_if
        self.path_factory = path_factory
        self.body_factory = body_factory
        self.probe = probe
        self.strict_expected = strict_expected
        self.http_expect_tagged = http_expect_tagged

def _normalize_expected(expected: Union[int, List[int], Tuple[int, ...]]) -> List[int]:
    if isinstance(expected, (list, tuple, set)):
        return list(expected)
    return [expected]

def resolve_expected_status(tc: TestCase, strict: bool) -> List[int]:
    if strict and tc.strict_expected is not None:
        return _normalize_expected(tc.strict_expected)
    raw = _normalize_expected(tc.expected_status)
    if not strict:
        return raw
    if all(s >= 400 for s in raw) and not any(s in (200, 201, 202, 204) for s in raw):
        return raw
    if getattr(tc, "http_expect_tagged", False):
        return [s for s in raw if s != 502]
    filtered = [s for s in raw if s not in (404, 409, 410, 502)]
    return filtered if filtered else raw

class TestRunner:
    def __init__(
        self,
        base_url: str,
        timeout: float = 15.0,
        verbose: bool = False,
        strict: bool = False,
    ):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.verbose = verbose
        self.strict = strict
        self.token: Optional[str] = None
        self.results: List[Dict[str, Any]] = []
        self.meta: Dict[str, Any] = {
            "http_skipped_unresolved": 0,
            "http_skipped_filter": 0,
        }

    def set_token(self, token: Optional[str]):
        self.token = token

    def check_connectivity(self) -> Tuple[bool, str]:
        """Performs a quick reachability probe before executing full test suites."""
        url = f"{self.base_url}/api/version"
        req = urllib.request.Request(url, headers={"Accept": "application/json"}, method="GET")
        try:
            with urllib.request.urlopen(req, timeout=3.0) as resp:
                if resp.status in [200, 401]:
                    return True, ""
                return False, f"Unexpected HTTP status {resp.status} on /api/version"
        except urllib.error.HTTPError as e:
            if e.code in [200, 401]:
                return True, ""
            return False, f"HTTP error {e.code}: {e.reason}"
        except Exception as e:
            return False, str(e)

    def request(
        self,
        method: str,
        path: str,
        body: Optional[Dict[str, Any]] = None,
        raw_body: Optional[str] = None,
        content_type: Optional[str] = None,
        token: Optional[str] = "__CURRENT__",
    ) -> Tuple[int, Dict[str, Any], float]:
        url = f"{self.base_url}{encode_request_path(path)}"
        req_headers = {"Accept": "application/json"}

        effective_token = self.token if token == "__CURRENT__" else token
        if effective_token:
            req_headers["Authorization"] = f"Bearer {effective_token}"

        data = None
        if raw_body is not None:
            data = raw_body.encode("utf-8")
            req_headers["Content-Type"] = content_type or "text/plain"
        elif body is not None:
            data = json.dumps(body).encode("utf-8")
            req_headers["Content-Type"] = "application/json"

        req = urllib.request.Request(url, data=data, headers=req_headers, method=method)
        start_time = time.time()
        status_code = 0
        resp_data = {}
        base_path = path.split("?")[0]
        is_stream = (
            base_path.rstrip("/").endswith("/stream")
            or base_path.rstrip("/") == "/api/events"
        )
        if base_path.rstrip("/").endswith("/launch"):
            req_timeout = max(self.timeout, 60.0)
        elif is_stream:
            req_timeout = min(self.timeout, 5.0)
        else:
            req_timeout = self.timeout

        def _do_request() -> Tuple[int, Dict[str, Any]]:
            nonlocal req
            with urllib.request.urlopen(req, timeout=req_timeout) as response:
                code = response.status
                payload: Dict[str, Any] = {}
                if is_stream:
                    try:
                        chunk = response.read(512)
                        if chunk:
                            payload = {"stream_preview": chunk[:120].decode("utf-8", errors="replace")}
                    except Exception:
                        pass
                else:
                    raw = response.read().decode("utf-8")
                    if raw:
                        try:
                            payload = json.loads(raw)
                        except Exception:
                            payload = {"raw": raw}
                return code, payload

        try:
            status_code, resp_data = _do_request()
        except urllib.error.HTTPError as e:
            status_code = e.code
            raw = e.read().decode("utf-8")
            resp_data = {}
            if raw:
                try:
                    resp_data = json.loads(raw)
                except Exception:
                    resp_data = {"raw": raw}
            if status_code == 429:
                time.sleep(1.0)
                try:
                    status_code, resp_data = _do_request()
                except urllib.error.HTTPError as retry_e:
                    status_code = retry_e.code
                    raw = retry_e.read().decode("utf-8")
                    if raw:
                        try:
                            resp_data = json.loads(raw)
                        except Exception:
                            resp_data = {"raw": raw}
        except Exception as e:
            duration = (time.time() - start_time) * 1000
            return 0, {"error": str(e)}, duration

        duration = (time.time() - start_time) * 1000
        return status_code, resp_data, duration

    def ensure_login(self, username: str, password: str) -> bool:
        if self.token:
            return True
        status, data, _ = self.request(
            "POST",
            "/api/sessions",
            body={"username": username, "password": password},
            token=None,
        )
        if status in [200, 201]:
            tok = data.get("data", {}).get("token")
            if tok:
                self.token = tok
                return True
        # One retry after brief restart / transient connection refused
        time.sleep(1.0)
        status, data, _ = self.request(
            "POST",
            "/api/sessions",
            body={"username": username, "password": password},
            token=None,
        )
        if status in [200, 201]:
            tok = data.get("data", {}).get("token")
            if tok:
                self.token = tok
                return True
        return False

    def _record_skip(self, tc: TestCase, path: str, reason: str) -> bool:
        self.results.append({
            "domain": tc.domain,
            "name": tc.name,
            "method": tc.method,
            "path": path,
            "expected": tc.expected_status,
            "actual": "SKIP",
            "duration": 0.0,
            "passed": False,
            "skipped": True,
            "msg": reason,
            "data": {},
        })
        print(f" {Colors.YELLOW} SKIP {Colors.RESET} {tc.method:<6} {path:<46} {'[skipped]':<14}")
        return False

    def run(self, tc: TestCase) -> bool:
        path = tc.path_factory() if tc.path_factory else tc.path
        if tc.skip_if and tc.skip_if():
            return self._record_skip(tc, path, "skipped (precondition)")

        resolved_token = tc.token() if callable(tc.token) else tc.token
        body = tc.body_factory() if tc.body_factory else tc.body
        status, data, duration = self.request(
            tc.method,
            path,
            body=body,
            raw_body=tc.raw_body,
            content_type=tc.content_type,
            token=resolved_token,
        )
        expected_list = resolve_expected_status(tc, self.strict)

        if self.strict and status == 502:
            passed = False
            v_msg = "502 Bad Gateway is never acceptable in --strict mode"
        elif self.strict and tc.probe and status == 404:
            return self._record_skip(tc, path, "probe route returned 404 in strict mode")
        elif status == 0:
            passed = False
            v_msg = data.get("error", "request failed")
        else:
            passed = status in expected_list
            v_msg = ""

        if passed and tc.validator:
            try:
                ok, v_msg = tc.validator(data)
                if not ok:
                    passed = False
            except Exception as ex:
                passed = False
                v_msg = f"Validator error: {ex}"

        self.results.append({
            "domain": tc.domain,
            "name": tc.name,
            "method": tc.method,
            "path": path,
            "expected": expected_list if self.strict else tc.expected_status,
            "actual": status,
            "duration": duration,
            "passed": passed,
            "skipped": False,
            "msg": v_msg if not passed else "",
            "data": data,
        })

        tag = f"{Colors.GREEN} PASS {Colors.RESET}" if passed else f"{Colors.RED} FAIL {Colors.RESET}"
        status_info = (
            f"[{status}]"
            if passed
            else f"[got {status}, expected {expected_list}]"
        )
        detail = f" - {v_msg}" if v_msg and not passed else ""
        print(f" {tag} {tc.method:<6} {path:<46} {status_info:<14} ({duration:5.1f}ms){detail}")

        if self.verbose or not passed:
            if tc.body:
                print(f"      {Colors.CYAN}Request Body:{Colors.RESET} {json.dumps(tc.body)}")
            if data:
                sample = json.dumps(data)
                if len(sample) > 200 and not self.verbose and passed:
                    sample = sample[:200] + "... (truncated)"
                print(f"      {Colors.CYAN}Response:{Colors.RESET} {sample}")

        return passed

def run_remote_deploy(mode: str) -> None:
    """bash .vscode/remote.sh build|push (VS Code tasks remote: build / remote: push)."""
    if mode not in ("build", "push"):
        raise ValueError(f"unsupported remote deploy mode: {mode}")
    if not REMOTE_BUILD_SCRIPT.is_file():
        print(f"{Colors.RED}Error:{Colors.RESET} {REMOTE_BUILD_SCRIPT} not found (run from repo root).")
        sys.exit(1)
    label = "build (remote compile)" if mode == "build" else "push (local compile + upload)"
    print(f"\n{Colors.BOLD}>>> Remote deploy: .vscode/remote.sh {mode} — {label}{Colors.RESET}\n")
    try:
        subprocess.run(
            ["bash", str(REMOTE_BUILD_SCRIPT), mode],
            cwd=str(REPO_ROOT),
            check=True,
        )
    except subprocess.CalledProcessError as exc:
        print(f"{Colors.RED}Remote deploy failed (exit {exc.returncode}).{Colors.RESET}")
        sys.exit(exc.returncode or 1)


def load_environment(env_name: str) -> Dict[str, Any]:
    if not ENV_FILE.exists():
        print(f"{Colors.RED}Error:{Colors.RESET} Environment file {ENV_FILE} not found.")
        sys.exit(1)
    with open(ENV_FILE, "r", encoding="utf-8") as f:
        # Keys starting with "_" are reviewer notes, not environments.
        envs = {k: v for k, v in json.load(f).items() if not str(k).startswith("_")}
    if env_name not in envs:
        available = ", ".join(envs.keys())
        print(f"{Colors.RED}Error:{Colors.RESET} Environment '{env_name}' not found in {ENV_FILE}. Available: {available}")
        sys.exit(1)
    return envs[env_name]

def _validators_for_http(test_node: str) -> Dict[Tuple[str, str], Callable[[Dict[str, Any]], Tuple[bool, str]]]:
    expected_settings_domains = {
        "platform", "auth", "connection", "file", "overview",
        "storage", "scheduler", "license", "agent", "alert", "desktop", "cert",
    }

    def validate_cluster_nodes(d: Dict[str, Any]) -> Tuple[bool, str]:
        items = d.get("data", {}).get("items", [])
        if not items:
            return False, "No nodes returned in list"
        node_map = {n["nodeId"]: n for n in items}
        if test_node and test_node not in node_map:
            return False, f"Expected node '{test_node}' not found in cluster list"
        for nid, n in node_map.items():
            if "isControl" in n or "isAgent" in n:
                return False, f"Removed role fields (isControl/isAgent) found on node {nid}"
            if "allowConnections" not in n or "maxConnections" not in n:
                return False, f"Missing connection guard fields on node {nid}"
        return True, ""

    def validate_settings_domains(d: Dict[str, Any]) -> Tuple[bool, str]:
        items = d.get("data")
        if not isinstance(items, list):
            return False, f"domains data must be a list, got {type(items).__name__}"
        actual = set(items)
        missing = expected_settings_domains - actual
        extra = actual - expected_settings_domains
        if missing or extra:
            parts = []
            if missing:
                parts.append(f"missing={sorted(missing)}")
            if extra:
                parts.append(f"extra={sorted(extra)}")
            return False, "settings domains mismatch: " + ", ".join(parts)
        return True, ""

    def validate_id_mappings(d: Dict[str, Any]) -> Tuple[bool, str]:
        data = d.get("data") or {}
        for key in ("users", "groups", "notFound"):
            if key not in data:
                return False, f"id-mappings response missing field: {key}"
        if not isinstance(data.get("users"), dict) or not isinstance(data.get("groups"), dict):
            return False, "id-mappings users/groups must be objects"
        nf = data.get("notFound")
        if not isinstance(nf, dict):
            return False, "id-mappings notFound must be an object with uids/gids"
        for key in ("uids", "gids"):
            if key not in nf or not isinstance(nf[key], list):
                return False, f"id-mappings notFound.{key} must be a list"
        return True, ""

    return {
        ("GET", "/api/admin/system/nodes"): validate_cluster_nodes,
        ("GET", "/api/admin/system/settings/domains"): validate_settings_domains,
        ("POST", "/api/users/id-mappings"): validate_id_mappings,
    }


def preflight_connection_lab_resources(runner: TestRunner, admin_user: str, admin_pwd: str) -> None:
    """Remove stale lab VNC/RDP resource rows so admin POST can recreate on testNode."""
    if not runner.token:
        runner.ensure_login(admin_user, admin_pwd)
    st, data, _ = runner.request("GET", "/api/admin/system/connections/resources", token="__CURRENT__")
    if st != 200:
        return
    for row in _list_rows(data):
        if not isinstance(row, dict):
            continue
        name = row.get("name")
        if name not in ("fixed-test-vnc-resource", "public-windows-rdp"):
            continue
        rid = row.get("id")
        if rid is not None:
            runner.request("DELETE", f"/api/admin/system/connections/resources/{rid}", token="__CURRENT__")


def ensure_lab_vnc_resource(runner: TestRunner, test_node: str) -> None:
    """Ensure public fixed-test-vnc-resource exists (needed when 08-connections runs alone)."""
    st, data, _ = runner.request("GET", "/api/admin/system/connections/resources", token="__CURRENT__")
    if st != 200:
        return
    for row in _list_rows(data):
        if isinstance(row, dict) and row.get("name") == "fixed-test-vnc-resource":
            return
    node = test_node or "agent7-1.dev.vm"
    runner.request(
        "POST",
        "/api/admin/system/connections/resources",
        body={
            "name": "fixed-test-vnc-resource",
            "protocol": "VNC",
            "nodes": [node],
            "nodeGroups": [],
            "maxPerUser": 1,
            "public": True,
        },
        token="__CURRENT__",
    )


def preflight_user_connections(runner: TestRunner, admin_user: str, admin_pwd: str, test_node: str = "") -> None:
    """Drop stale user connections so VNC launch is not rejected with 409 (display occupied)."""
    if not runner.token:
        runner.ensure_login(admin_user, admin_pwd)
    runner.request("POST", "/api/admin/system/nodes/refresh", token="__CURRENT__")
    runner.request("POST", "/api/admin/system/connections/cleanup", token="__CURRENT__")
    runner.request("POST", "/api/connections/refresh", token="__CURRENT__")
    ensure_lab_vnc_resource(runner, test_node)
    ast, adata, _ = runner.request("GET", "/api/admin/system/connections", token="__CURRENT__")
    if ast == 200:
        aids = [
            row.get("id")
            for row in _list_rows(adata)
            if isinstance(row, dict) and row.get("id") is not None
        ]
        if aids:
            runner.request(
                "POST",
                "/api/admin/system/connections/batch-delete",
                body={"ids": aids},
                token="__CURRENT__",
            )
    st, data, _ = runner.request("GET", "/api/connections", token="__CURRENT__")
    if st != 200:
        return
    ids = [
        row.get("id")
        for row in _list_rows(data)
        if isinstance(row, dict) and row.get("id") is not None
    ]
    if ids:
        runner.request("POST", "/api/connections/batch-delete", body={"ids": ids}, token="__CURRENT__")


def execute_http_file_suite(
    runner: TestRunner,
    env_data: Dict[str, Any],
    admin_user: str,
    admin_pwd: str,
    test_node: str,
    domain_filter: Optional[str] = None,
    fail_fast: bool = False,
) -> int:
    """Run all non-@manual blocks from test/portal/*.http in dependency order."""
    runner.meta["_admin_user"] = admin_user
    runner.meta["_admin_pwd"] = admin_pwd
    plan, vars_map = build_http_execution_plan(env_data)
    validators = _validators_for_http(test_node)
    skipped = 0
    connection_preflight_done = False
    connection_admin_preflight_done = False
    for req in plan:
        stem = req.source_file.replace(".http", "")
        if domain_filter and domain_filter not in (stem, req.source_file):
            runner.meta["http_skipped_filter"] = int(runner.meta.get("http_skipped_filter", 0)) + 1
            continue
        if req.token_mode == "current" and not runner.token:
            runner.ensure_login(admin_user, admin_pwd)
        if req.source_file == "08-connections-system-admin.http" and not connection_admin_preflight_done:
            connection_admin_preflight_done = True
            preflight_connection_lab_resources(runner, admin_user, admin_pwd)
        if req.source_file == "08-connections.http" and not connection_preflight_done:
            connection_preflight_done = True
            preflight_user_connections(runner, admin_user, admin_pwd, test_node)
        resolved = resolve_request(req, vars_map)
        if not resolved:
            skipped += 1
            runner.meta["http_skipped_unresolved"] = int(runner.meta.get("http_skipped_unresolved", 0)) + 1
            if runner.verbose:
                print(
                    f" {Colors.YELLOW} SKIP {Colors.RESET} {req.method:<6} "
                    f"{req.path_template:<46} [unresolved template vars]"
                )
            continue
        method, path, body, raw_body, content_type, expect, token_mode = resolved
        path_key = path.split("?")[0]
        tok = None if token_mode == "none" else "__CURRENT__"
        if token_mode == "literal":
            auth = req.headers.get("Authorization", "")
            if auth.lower().startswith("bearer "):
                tok = auth[7:].strip()
        tc = TestCase(
            stem,
            req.title,
            method,
            path,
            body=body,
            raw_body=raw_body,
            content_type=content_type,
            token=tok,
            expected_status=expect,
            validator=validators.get((method, path_key)),
            probe=req.probe,
            http_expect_tagged=req.expect_tagged,
        )
        if not runner.run(tc) and fail_fast:
            return skipped
        last = runner.results[-1]
        if last["passed"]:
            apply_capture_rules(method, path, last.get("data") or {}, vars_map)
            if vars_map.get("accessToken"):
                runner.set_token(vars_map["accessToken"])
        if (
            last.get("actual") == 409
            and method == "POST"
            and path_key == "/api/admin/system/apps"
        ):
            st, data, _ = runner.request("GET", "/api/admin/system/apps", token="__CURRENT__")
            if st == 200:
                apply_capture_rules("GET", "/api/admin/system/apps", data, vars_map)
    return skipped


def build_test_suite(runner: TestRunner, admin_user: str, admin_pwd: str, normal_user: str, normal_pwd: str, test_node: str) -> List[Tuple[str, List[TestCase]]]:
    suites = []

    auth_guard_cases = [
        TestCase(
            "auth_guard",
            "Login with Invalid Password",
            "POST",
            "/api/sessions",
            body={"username": admin_user, "password": "wrong_password_xyz_999"},
            token=None,
            expected_status=[401, 403],
        ),
        TestCase(
            "auth_guard",
            "Protected Endpoint Without Token",
            "GET",
            "/api/admin/system/nodes",
            token=None,
            expected_status=[401, 403],
        ),
    ]
    suites.append(("Authentication Negative & Security Guard", auth_guard_cases))

    # RBAC runs after test/portal/*.http in main()
    user_tok_holder = [None]
    normal_user_has_admin_api = [False]

    def capture_user_token(d):
        tok = d.get("data", {}).get("token")
        if tok:
            user_tok_holder[0] = tok
            st, _, _ = runner.request("GET", "/api/admin/system/nodes", token=tok)
            normal_user_has_admin_api[0] = st == 200
            return True, ""
        return False, "Failed to capture normal user token"

    def skip_if_normal_user_is_privileged():
        return normal_user_has_admin_api[0]

    rbac_cases = [
        TestCase(
            "rbac",
            f"Normal User Login ({normal_user})",
            "POST",
            "/api/sessions",
            body={"username": normal_user, "password": normal_pwd},
            token=None,
            expected_status=[200, 201],
            validator=capture_user_token,
        ),
        TestCase(
            "rbac",
            "Normal User Get Profile",
            "GET",
            "/api/users/me",
            token=lambda: user_tok_holder[0],
            expected_status=200,
        ),
        TestCase(
            "rbac",
            "Normal User List Apps",
            "GET",
            "/api/apps",
            token=lambda: user_tok_holder[0],
            expected_status=200,
        ),
        TestCase(
            "rbac",
            "Normal User Access Admin Nodes (Forbidden)",
            "GET",
            "/api/admin/system/nodes",
            token=lambda: user_tok_holder[0],
            expected_status=403,
            skip_if=skip_if_normal_user_is_privileged,
        ),
        TestCase(
            "rbac",
            "Normal User Access Admin Settings (Forbidden)",
            "GET",
            "/api/admin/system/settings/platform",
            token=lambda: user_tok_holder[0],
            expected_status=403,
            skip_if=skip_if_normal_user_is_privileged,
        ),
        TestCase(
            "rbac",
            "Normal User Modify Scopes (Forbidden)",
            "PUT",
            f"/api/admin/security/scopes/{normal_user}",
            body={"scopes": 0},
            token=lambda: user_tok_holder[0],
            expected_status=403,
        ),
        TestCase(
            "rbac",
            "Normal User Modify Role (Forbidden)",
            "PUT",
            f"/api/admin/security/users/{normal_user}/role",
            body={"role": "systemAdmin"},
            token=lambda: user_tok_holder[0],
            expected_status=403,
        ),
    ]
    suites.append(("RBAC Scope & Privilege Boundary Isolation", rbac_cases))

    return suites


def build_python_extension_suites(
    runner: TestRunner,
    test_node: str,
    test_app_id: str,
    test_job_id: str,
    test_group: str,
    normal_user: str,
) -> List[Tuple[str, List[TestCase]]]:
    from cases_extended import build_extended_suites
    from cases_mutations import build_mutation_suites

    suites: List[Tuple[str, List[TestCase]]] = []
    suites.extend(
        build_extended_suites(
            runner,
            test_node=test_node,
            test_app_id=test_app_id,
            test_job_id=test_job_id,
            test_group=test_group,
            normal_user=normal_user,
        )
    )
    suites.extend(build_mutation_suites(runner))
    return suites


def run_python_extension_suites(
    runner: TestRunner,
    suites: List[Tuple[str, List[TestCase]]],
    admin_user: str,
    admin_pwd: str,
    domain_filter: Optional[str],
    fail_fast: bool,
) -> bool:
    """Return False if fail-fast should stop the run."""
    for suite_name, cases in suites:
        filtered_cases = [c for c in cases if not domain_filter or c.domain == domain_filter]
        if not filtered_cases:
            continue
        print(f"\n{Colors.BOLD}>>> {suite_name}{Colors.RESET}")
        for tc in filtered_cases:
            if tc.domain not in ("public", "auth_guard") and not runner.token:
                runner.ensure_login(admin_user, admin_pwd)
            if not runner.run(tc) and fail_fast:
                print(f"\n{Colors.RED}[FAIL-FAST] Terminating suite early due to test failure.{Colors.RESET}")
                return False
    return True

def _http_file_inventory() -> Tuple[int, int, int]:
    """Return (total_blocks, manual_blocks, auto_blocks)."""
    all_reqs = load_all_http_requests()
    manual = sum(1 for r in all_reqs if r.manual)
    auto = len(all_reqs) - manual
    return len(all_reqs), manual, auto


def build_run_summary(
    runner: TestRunner,
    *,
    env_name: str,
    base_url: str,
    admin_user: str,
    normal_user: str,
    domain_filter: Optional[str],
    wall_seconds: float,
) -> Dict[str, Any]:
    results = runner.results
    skipped_run = sum(1 for r in results if r.get("skipped") or r.get("actual") == "SKIP")
    failed_rows = [r for r in results if not r["passed"] and not r.get("skipped")]
    passed_rows = [r for r in results if r["passed"]]
    total_executed = len(results)
    failed = len(failed_rows)
    passed = len(passed_rows)

    by_domain: Dict[str, Dict[str, int]] = {}
    for r in results:
        dom = r.get("domain") or "unknown"
        bucket = by_domain.setdefault(dom, {"pass": 0, "fail": 0, "skip": 0})
        if r.get("actual") == "SKIP":
            bucket["skip"] += 1
        elif r["passed"]:
            bucket["pass"] += 1
        else:
            bucket["fail"] += 1

    http_total, http_manual, http_auto = _http_file_inventory()
    req_ms = sum(r.get("duration", 0) for r in results)
    avg_ms = req_ms / total_executed if total_executed else 0.0

    return {
        "ok": failed == 0,
        "env": env_name,
        "base_url": base_url,
        "admin_user": admin_user,
        "normal_user": normal_user,
        "domain_filter": domain_filter,
        "wall_seconds": round(wall_seconds, 1),
        "executed": total_executed,
        "passed": passed,
        "failed": failed,
        "skipped_in_run": skipped_run,
        "avg_latency_ms": round(avg_ms, 1),
        "http_blocks_total": http_total,
        "http_blocks_manual": http_manual,
        "http_blocks_auto": http_auto,
        "http_skipped_unresolved": int(runner.meta.get("http_skipped_unresolved", 0)),
        "http_skipped_filter": int(runner.meta.get("http_skipped_filter", 0)),
        "by_domain": by_domain,
        "failures": [
            {
                "domain": r.get("domain"),
                "name": r.get("name"),
                "method": r.get("method"),
                "path": r.get("path"),
                "expected": r.get("expected"),
                "actual": r.get("actual"),
                "msg": r.get("msg"),
            }
            for r in failed_rows
        ],
    }


def print_run_summary(summary: Dict[str, Any], json_mode: bool = False) -> None:
    if json_mode:
        return

    status = "PASS" if summary["ok"] else "FAIL"
    status_color = Colors.GREEN if summary["ok"] else Colors.RED
    filt = summary.get("domain_filter")
    filt_line = f"  Domain filter : {filt}\n" if filt else ""

    print(f"\n{Colors.BOLD}{'=' * 80}{Colors.RESET}")
    print(f"  {Colors.CYAN}RUN SUMMARY{Colors.RESET}")
    print(f"{Colors.BOLD}{'=' * 80}{Colors.RESET}")
    print(
        f"  Status        : {status_color}{status}{Colors.RESET} — "
        f"{summary['passed']} passed, {summary['failed']} failed, "
        f"{summary['skipped_in_run']} skipped (precondition) in {summary['wall_seconds']}s"
    )
    print(f"  Target        : {summary['base_url']}  (env: {summary['env']})")
    print(f"  Accounts      : admin={summary['admin_user']}, rbac={summary['normal_user']}")
    print(filt_line, end="")
    print(
        f"  .http plan    : {summary['http_blocks_auto']} auto-run blocks, "
        f"{summary['http_blocks_manual']} @manual (not run); "
        f"{summary['http_skipped_unresolved']} skipped (unresolved vars)"
    )
    if summary["http_skipped_filter"]:
        print(f"  .http filter  : {summary['http_skipped_filter']} block(s) not in domain filter")
    print(f"  Avg latency   : {summary['avg_latency_ms']} ms / executed request")

    by_domain: Dict[str, Dict[str, int]] = summary["by_domain"]
    if by_domain:
        print(f"\n  {Colors.BOLD}By module:{Colors.RESET}")
        rows = sorted(
            by_domain.items(),
            key=lambda item: (-item[1]["fail"], -item[1]["pass"], item[0]),
        )
        for dom, counts in rows:
            mark = ""
            if counts["fail"]:
                mark = f" {Colors.RED}← failures{Colors.RESET}"
            print(
                f"    {dom:<28} pass {counts['pass']:>4}  fail {counts['fail']:>3}  "
                f"skip {counts['skip']:>3}{mark}"
            )

    failures = summary["failures"]
    if failures:
        print(f"\n  {Colors.RED}{Colors.BOLD}Failed requests:{Colors.RESET}")
        max_show = 25
        for r in failures[:max_show]:
            msg = f" — {r['msg']}" if r.get("msg") else ""
            print(
                f"    - [{r['method']} {r['path']}] "
                f"expected {r['expected']}, got {r['actual']}{msg}"
            )
        if len(failures) > max_show:
            print(f"    ... and {len(failures) - max_show} more")

    print(f"{Colors.BOLD}{'=' * 80}{Colors.RESET}\n")
    if summary["ok"]:
        print(f"{Colors.GREEN}All executed Portal API checks passed.{Colors.RESET}\n")
    else:
        print(
            f"{Colors.RED}Fix failures above, then re-run: "
            f"bash test/portal/run.sh --no-build{Colors.RESET}\n"
        )


def get_available_domains(suites: List[Tuple[str, List[TestCase]]]) -> List[str]:
    seen = set()
    result = []
    for _, cases in suites:
        for c in cases:
            if c.domain not in seen:
                seen.add(c.domain)
                result.append(c.domain)
    for name in HTTP_FILE_ORDER:
        stem = name.replace(".http", "")
        if stem not in seen:
            seen.add(stem)
            result.append(stem)
    return result

def main():
    parser = argparse.ArgumentParser(description="Design Cloud Engine Portal HTTP API Test Runner")
    parser.add_argument(
        "--env",
        default=DEFAULT_ENV,
        help=f"Environment name from http-client.env.json (default: {DEFAULT_ENV})",
    )
    deploy_group = parser.add_mutually_exclusive_group()
    deploy_group.add_argument(
        "--remote-build",
        action="store_true",
        help="Deploy via .vscode/remote.sh build before tests (default in run_* wrapper scripts)",
    )
    deploy_group.add_argument(
        "--remote-push",
        action="store_true",
        help="Deploy via .vscode/remote.sh push (local compile + upload)",
    )
    parser.add_argument("--addr", help="Override base URL (e.g. http://10.1.1.100:1234); skips env file for addr/credentials except defaults")
    parser.add_argument("--username", help="Override admin username")
    parser.add_argument("--password", help="Override admin password")
    parser.add_argument("--normal-user", default="test1", help="Normal user for RBAC boundary testing (default: test1)")
    parser.add_argument("--normal-pwd", default="pwd_test1", help="Normal user password (default: pwd_test1)")
    parser.add_argument("--domain", help="Filter by test domain (use --list-domains to view all available domains)")
    parser.add_argument("--list-domains", action="store_true", help="List all available test domain tags and exit")
    parser.add_argument("--timeout", type=float, default=15.0, help="HTTP request timeout in seconds (default: 15.0)")
    parser.add_argument("--fail-fast", action="store_true", help="Stop execution immediately on first test failure")
    parser.add_argument("--verbose", "-v", action="store_true", help="Print request body and full response payload")
    parser.add_argument("--no-color", action="store_true", help="Disable colored terminal output")
    parser.add_argument("--json", action="store_true", help="Output summary in JSON format")
    parser.add_argument(
        "--strict",
        action="store_true",
        help="Contract mode: drop 404/502 smoke alternates; probe routes skip on 404; 502 always fails",
    )
    parser.add_argument(
        "--allow-skip",
        action="store_true",
        help="With --strict, do not exit non-zero when cases were skipped",
    )
    parser.add_argument(
        "--insecure",
        action="store_true",
        help="Disable TLS certificate verification (for self-signed lab certs)",
    )

    args = parser.parse_args()

    if args.insecure:
        ssl._create_default_https_context = ssl._create_unverified_context

    if args.no_color or not sys.stdout.isatty():
        Colors.disable()

    if args.remote_build:
        run_remote_deploy("build")
    elif args.remote_push:
        run_remote_deploy("push")

    if args.addr:
        env_data = {"addr": args.addr}
    else:
        env_data = load_environment(args.env)

    base_url = args.addr or env_data.get("addr") or f"http://{env_data.get('host', 'localhost')}:1234"
    admin_user = args.username or env_data.get("username", "hpcadmin")
    admin_pwd = args.password or env_data.get("password", "pwd_hpcadmin")
    normal_user = args.normal_user or env_data.get("normalUser", "test1")
    normal_pwd = args.normal_pwd or "pwd_test1"
    test_node = env_data.get("testNode", "cent7")
    test_app_id = str(env_data.get("testAppId", "1"))
    test_job_id = str(env_data.get("testJobId", "0"))
    test_group = str(env_data.get("testGroup", "cad"))

    runner = TestRunner(base_url, timeout=args.timeout, verbose=args.verbose, strict=args.strict)
    suites = build_test_suite(runner, admin_user, admin_pwd, normal_user, normal_pwd, test_node)
    extension_suites = build_python_extension_suites(
        runner, test_node, test_app_id, test_job_id, test_group, normal_user
    )

    if args.list_domains:
        available_domains = get_available_domains(suites)
        print("Available test domains:")
        for d in available_domains:
            print(f"  - {d}")
        sys.exit(0)

    print(f"\n{Colors.BOLD}================================================================================{Colors.RESET}")
    print(f"  {Colors.CYAN}Design Cloud Engine - Portal HTTP API Test Suite{Colors.RESET}")
    print(f"  Target Server: {Colors.YELLOW}{base_url}{Colors.RESET}")
    print(f"  Environment  : {args.env} (Admin: {admin_user}, Normal: {normal_user})")
    if args.strict:
        print(f"  Mode         : {Colors.CYAN}STRICT{Colors.RESET} (skips fail unless --allow-skip)")
    print(f"{Colors.BOLD}================================================================================{Colors.RESET}\n")

    # Fast connectivity check
    print(f"[*] Probing connectivity to {base_url} ...", end=" ", flush=True)
    is_up, probe_err = runner.check_connectivity()
    if not is_up:
        print(f"{Colors.RED}UNREACHABLE{Colors.RESET}")
        print(f"\n{Colors.RED}Connection Failure:{Colors.RESET} Cannot connect to server at {base_url}")
        print(f"  Details: {probe_err}")
        print(f"  Please ensure Design Cloud Engine server is running on {base_url}.")
        print(f"  Remote lab: bash test/portal/run.sh  (default: remote build + test)")
        print(f"  Local compile upload: bash test/portal/run.sh --push")
        print(f"  Or: python3 test/portal/run_http_tests.py --remote-build|--remote-push --env {DEFAULT_ENV}\n")
        sys.exit(1)
    print(f"{Colors.GREEN}ONLINE{Colors.RESET}")

    run_started = time.time()
    http_domain_filter = args.domain if args.domain and args.domain not in ("auth_guard", "rbac") else None

    def run_suite_batch(include_rbac: bool) -> bool:
        """Return False if fail-fast should stop the run."""
        for suite_name, cases in suites:
            if include_rbac:
                if "RBAC" not in suite_name:
                    continue
            else:
                if "RBAC" in suite_name:
                    continue
            filtered_cases = [c for c in cases if not args.domain or c.domain == args.domain]
            if not filtered_cases:
                continue
            print(f"\n{Colors.BOLD}>>> {suite_name}{Colors.RESET}")
            for tc in filtered_cases:
                passed = runner.run(tc)
                if not passed and args.fail_fast:
                    print(f"\n{Colors.RED}[FAIL-FAST] Terminating suite early due to test failure.{Colors.RESET}")
                    return False
        return True

    abort_after_fail_fast = False

    # 先跑 .http 与 RBAC，最后再跑 auth_guard，避免错误密码/无效 token 触发 IP 封禁导致全套件失败。
    if not args.domain or args.domain not in ("auth_guard", "rbac"):
        print(f"\n{Colors.BOLD}>>> Portal API from test/portal/*.http (non-@manual){Colors.RESET}")
        skipped = execute_http_file_suite(
            runner,
            env_data,
            admin_user,
            admin_pwd,
            test_node,
            domain_filter=http_domain_filter,
            fail_fast=args.fail_fast,
        )
        if skipped and not runner.verbose:
            print(
                f"  {Colors.YELLOW}Note:{Colors.RESET} {skipped} .http block(s) skipped "
                f"(unresolved {{vars}}); see RUN SUMMARY."
            )
        if args.fail_fast and any(not r["passed"] and not r.get("skipped") for r in runner.results):
            abort_after_fail_fast = True

    if not abort_after_fail_fast and (not args.domain or args.domain not in ("auth_guard", "rbac")):
        if not run_python_extension_suites(
            runner,
            extension_suites,
            admin_user,
            admin_pwd,
            args.domain,
            args.fail_fast,
        ):
            abort_after_fail_fast = True

    if not abort_after_fail_fast and (not args.domain or args.domain == "rbac"):
        if not run_suite_batch(include_rbac=True):
            abort_after_fail_fast = True

    if not abort_after_fail_fast and (not args.domain or args.domain == "auth_guard"):
        if not run_suite_batch(include_rbac=False):
            abort_after_fail_fast = True

    wall_seconds = time.time() - run_started
    summary = build_run_summary(
        runner,
        env_name=args.env if not args.addr else "(addr override)",
        base_url=base_url,
        admin_user=admin_user,
        normal_user=normal_user,
        domain_filter=args.domain,
        wall_seconds=wall_seconds,
    )
    print_run_summary(summary, json_mode=args.json)

    if args.json:
        print(json.dumps({"summary": summary, "results": runner.results}, indent=2))

    strict_skip_violation = args.strict and summary["skipped_in_run"] > 0 and not args.allow_skip
    ok = summary["ok"] and not strict_skip_violation
    if strict_skip_violation:
        print(
            f"{Colors.RED}Strict mode: {summary['skipped_in_run']} skipped case(s); "
            f"use --allow-skip to permit skips{Colors.RESET}\n"
        )
    sys.exit(0 if ok else 1)

if __name__ == "__main__":
    main()
