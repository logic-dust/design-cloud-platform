#!/usr/bin/env python3
"""
Design Cloud Engine - Portal HTTP API Test Runner
Directly executes end-to-end integration tests using definitions and configurations
from test/api/http-client.env.json and corresponding *.http modules.
"""

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple, Union

SCRIPT_DIR = Path(__file__).resolve().parent
ENV_FILE = SCRIPT_DIR / "http-client.env.json"

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
        token: Optional[Union[str, Callable[[], Optional[str]]]] = "__CURRENT__",
        expected_status: Union[int, List[int], Tuple[int, ...]] = 200,
        validator: Optional[Callable[[Dict[str, Any]], Tuple[bool, str]]] = None,
    ):
        self.domain = domain
        self.name = name
        self.method = method
        self.path = path
        self.body = body
        self.token = token
        self.expected_status = expected_status
        self.validator = validator

class TestRunner:
    def __init__(self, base_url: str, timeout: float = 15.0, verbose: bool = False):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.verbose = verbose
        self.token: Optional[str] = None
        self.results: List[Dict[str, Any]] = []

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
        token: Optional[str] = "__CURRENT__",
    ) -> Tuple[int, Dict[str, Any], float]:
        url = f"{self.base_url}{path}"
        req_headers = {"Accept": "application/json"}

        effective_token = self.token if token == "__CURRENT__" else token
        if effective_token:
            req_headers["Authorization"] = f"Bearer {effective_token}"

        data = None
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            req_headers["Content-Type"] = "application/json"

        req = urllib.request.Request(url, data=data, headers=req_headers, method=method)
        start_time = time.time()
        status_code = 0
        resp_data = {}

        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as response:
                status_code = response.status
                raw = response.read().decode("utf-8")
                if raw:
                    try:
                        resp_data = json.loads(raw)
                    except Exception:
                        resp_data = {"raw": raw}
        except urllib.error.HTTPError as e:
            status_code = e.code
            raw = e.read().decode("utf-8")
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
        return False

    def run(self, tc: TestCase) -> bool:
        resolved_token = tc.token() if callable(tc.token) else tc.token
        status, data, duration = self.request(tc.method, tc.path, body=tc.body, token=resolved_token)
        expected_list = (
            list(tc.expected_status)
            if isinstance(tc.expected_status, (list, tuple, set))
            else [tc.expected_status]
        )
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
            "path": tc.path,
            "expected": tc.expected_status,
            "actual": status,
            "duration": duration,
            "passed": passed,
            "msg": v_msg if not passed else "",
            "data": data,
        })

        tag = f"{Colors.GREEN} PASS {Colors.RESET}" if passed else f"{Colors.RED} FAIL {Colors.RESET}"
        status_info = (
            f"[{status}]"
            if passed
            else f"[got {status}, expected {tc.expected_status}]"
        )
        detail = f" - {v_msg}" if v_msg and not passed else ""
        print(f" {tag} {tc.method:<6} {tc.path:<46} {status_info:<14} ({duration:5.1f}ms){detail}")

        if self.verbose or not passed:
            if tc.body:
                print(f"      {Colors.CYAN}Request Body:{Colors.RESET} {json.dumps(tc.body)}")
            if data:
                sample = json.dumps(data)
                if len(sample) > 200 and not self.verbose and passed:
                    sample = sample[:200] + "... (truncated)"
                print(f"      {Colors.CYAN}Response:{Colors.RESET} {sample}")

        return passed

def load_environment(env_name: str) -> Dict[str, Any]:
    if not ENV_FILE.exists():
        print(f"{Colors.RED}Error:{Colors.RESET} Environment file {ENV_FILE} not found.")
        sys.exit(1)
    with open(ENV_FILE, "r", encoding="utf-8") as f:
        envs = json.load(f)
    if env_name not in envs:
        available = ", ".join(envs.keys())
        print(f"{Colors.RED}Error:{Colors.RESET} Environment '{env_name}' not found in {ENV_FILE}. Available: {available}")
        sys.exit(1)
    return envs[env_name]

def build_test_suite(runner: TestRunner, admin_user: str, admin_pwd: str, normal_user: str, normal_pwd: str, test_node: str) -> List[Tuple[str, List[TestCase]]]:
    suites = []

    # 1. Public APIs (01-public-api.http)
    public_cases = [
        TestCase(
            "public",
            "Get System Version",
            "GET",
            "/api/version",
            token=None,
            expected_status=200,
            validator=lambda d: (bool(d.get("data", {}).get("version")), "Version string missing in response"),
        ),
        TestCase(
            "public",
            "Get Latest Desktop Client",
            "GET",
            "/api/version/desktop/latest",
            token=None,
            expected_status=[200, 404],
        ),
    ]
    suites.append(("Public APIs (01-public-api.http)", public_cases))

    # 2. Auth Guard & Negative Tests
    auth_guard_cases = [
        TestCase(
            "auth_guard",
            "Login with Invalid Password",
            "POST",
            "/api/sessions",
            body={"username": admin_user, "password": "wrong_password_xyz_999"},
            token=None,
            expected_status=401,
        ),
        TestCase(
            "auth_guard",
            "Protected Endpoint Without Token",
            "GET",
            "/api/admin/system/nodes",
            token=None,
            expected_status=401,
        ),
        TestCase(
            "auth_guard",
            "Protected Endpoint With Invalid Token",
            "GET",
            "/api/admin/system/nodes",
            token="invalid_bearer_token_xyz",
            expected_status=401,
        ),
    ]
    suites.append(("Authentication Negative & Security Guard", auth_guard_cases))

    # 3. Session & Identity Lifecycle (02-session.http)
    def capture_admin_token(d):
        tok = d.get("data", {}).get("token")
        if tok:
            runner.set_token(tok)
            return True, ""
        return False, "Failed to capture token from login response"

    session_cases = [
        TestCase(
            "session",
            f"Admin Login ({admin_user})",
            "POST",
            "/api/sessions",
            body={"username": admin_user, "password": admin_pwd},
            token=None,
            expected_status=[200, 201],
            validator=capture_admin_token,
        ),
        TestCase("session", "Get Current Session", "GET", "/api/sessions/current", expected_status=200),
        TestCase("session", "Renew Current Session", "POST", "/api/sessions/current/renew", expected_status=200),
        TestCase("session", "Get User Profile", "GET", "/api/users/me", expected_status=200),
    ]
    suites.append(("Session & Identity Lifecycle (02-session.http)", session_cases))

    # 4. Security Admin Governance (03-bans, 04-sessions, 05-access)
    security_cases = [
        TestCase("security", "Admin List Security Bans", "GET", "/api/admin/security/bans", expected_status=200),
        TestCase("security", "Admin List Active Sessions", "GET", "/api/admin/security/sessions", expected_status=200),
        TestCase("security", "Admin Get Password Policy", "GET", "/api/admin/security/policies/password", expected_status=200),
        TestCase("security", "Admin Get MFA Policy", "GET", "/api/admin/security/policies/mfa", expected_status=200),
        TestCase("security", f"Admin Get User Scopes ({admin_user})", "GET", f"/api/admin/security/scopes/{admin_user}", expected_status=200),
    ]
    suites.append(("Security Admin Governance (03-bans / 04-sessions / 05-access)", security_cases))

    # 5. Cluster Nodes Domain (12-nodes-systemadmin.http)
    def validate_cluster_nodes(d):
        items = d.get("data", {}).get("items", [])
        if not items:
            return False, "No nodes returned in list"
        node_map = {n["nodeId"]: n for n in items}
        if test_node and test_node not in node_map:
            return False, f"Expected node '{test_node}' not found in cluster list"
        for nid, n in node_map.items():
            if "isControl" in n or "isAgent" in n:
                return False, f"Legacy role fields (isControl/isAgent) found on node {nid}"
            if "allowConnections" not in n or "maxConnections" not in n:
                return False, f"Missing connection guard fields on node {nid}"
        return True, ""

    node_cases = [
        TestCase(
            "nodes",
            "List Cluster Nodes",
            "GET",
            "/api/admin/system/nodes",
            expected_status=200,
            validator=validate_cluster_nodes,
        ),
        TestCase("nodes", f"Get Node Detail ({test_node})", "GET", f"/api/admin/system/nodes/{test_node}", expected_status=200),
        TestCase("nodes", f"Get Node Status ({test_node})", "GET", f"/api/admin/system/nodes/{test_node}/status", expected_status=200),
        TestCase("nodes", f"Get Node Settings ({test_node})", "GET", f"/api/admin/system/nodes/{test_node}/settings", expected_status=200),
        TestCase("nodes", f"Get Node Settings Schema ({test_node})", "GET", f"/api/admin/system/nodes/{test_node}/settings/schema", expected_status=200),
        TestCase("nodes", "Trigger Immediate Nodes Scan", "POST", "/api/admin/system/nodes/refresh", expected_status=[200, 202]),
    ]
    suites.append(("Nodes Domain (12-nodes-systemadmin.http)", node_cases))

    # 6. Applications & Schedulers (06-apps, 07-scheduler)
    app_cases = [
        TestCase("apps", "User List Applications", "GET", "/api/apps", expected_status=200),
        TestCase("apps", "Admin List Applications", "GET", "/api/admin/system/apps", expected_status=200),
        TestCase("scheduler", "User List Scheduler Queues", "GET", "/api/scheduler/queues", expected_status=200),
        TestCase("scheduler", "User List Scheduler Jobs", "GET", "/api/scheduler/jobs", expected_status=200),
        TestCase("scheduler", "Admin List All Scheduler Jobs", "GET", "/api/admin/system/scheduler/jobs", expected_status=200),
    ]
    suites.append(("Applications & Scheduler (06-apps / 07-scheduler)", app_cases))

    # 7. Connections & Remote Access (08-connections, 09-proxy)
    conn_cases = [
        TestCase("connections", "Admin List All Connections", "GET", "/api/admin/system/connections", expected_status=200),
        TestCase("connections", "User List Connections", "GET", "/api/connections", expected_status=200),
        TestCase("connections", "Admin Refresh Connections", "POST", "/api/admin/system/connections/refresh", expected_status=[200, 202]),
    ]
    suites.append(("Connections Domain (08-connections / 09-proxy)", conn_cases))

    # 8. Files & Storage Domain (10-files, 19-status-storage)
    file_cases = [
        TestCase("storage", "Admin Get Shared Storage Snapshot", "GET", "/api/admin/system/storage", expected_status=200),
        TestCase("files", "User List File Download Requests", "GET", "/api/files/download-requests", expected_status=200),
        TestCase("files", "Admin List Pending File Requests", "GET", "/api/admin/security/files/download-requests", expected_status=200),
    ]
    suites.append(("Files & Storage Domain (10-files / 19-status-storage)", file_cases))

    # 9. Users & Groups Directory (14-users, 15-users-systemadmin)
    user_cases = [
        TestCase("users", "User Search/List Directory", "GET", "/api/users", expected_status=200),
        TestCase("users", "Admin List Managed Users", "GET", "/api/admin/system/users", expected_status=200),
        TestCase("users", "Admin List Directory Groups", "GET", "/api/admin/system/users/groups", expected_status=200),
    ]
    suites.append(("Users & Directory (14-users / 15-users-systemadmin)", user_cases))

    # 10. Runtime System Settings (16-settings, 16-settings-systemadmin, 16-settings-alert)
    def validate_settings_domains(d):
        items = d.get("data", {}).get("domains", [])
        expected_domains = {"platform", "desktop", "alert", "connection", "cert", "session", "node", "storage"}
        missing = expected_domains - set(items)
        if missing:
            return False, f"Missing expected settings domains: {missing}"
        return True, ""

    settings_cases = [
        TestCase("settings", "User Get Basic Settings", "GET", "/api/settings", expected_status=200),
        TestCase("settings", "Admin List Settings Domains", "GET", "/api/admin/system/settings/domains", expected_status=200, validator=validate_settings_domains),
        # Platform domain
        TestCase("settings", "Get Platform Settings", "GET", "/api/admin/system/settings/platform", expected_status=200),
        TestCase("settings", "Get Platform Settings Schema", "GET", "/api/admin/system/settings/platform/schema", expected_status=200),
        # Desktop domain
        TestCase("settings", "Get Desktop Client Settings", "GET", "/api/admin/system/settings/desktop", expected_status=200),
        TestCase("settings", "Get Desktop Client Schema", "GET", "/api/admin/system/settings/desktop/schema", expected_status=200),
        # Alert domain
        TestCase("settings", "Get Alert Threshold Settings", "GET", "/api/admin/system/settings/alert", expected_status=200),
        TestCase("settings", "Get Alert Threshold Schema", "GET", "/api/admin/system/settings/alert/schema", expected_status=200),
        # Connection & Cert
        TestCase("settings", "Get Connection Settings", "GET", "/api/admin/system/settings/connection", expected_status=200),
        TestCase("settings", "Get Connection Settings Schema", "GET", "/api/admin/system/settings/connection/schema", expected_status=200),
        TestCase("settings", "Get Cert Settings", "GET", "/api/admin/system/settings/cert", expected_status=200),
        TestCase("settings", "Get Cert Settings Schema", "GET", "/api/admin/system/settings/cert/schema", expected_status=200),
        # Session, Node, Security, Storage
        TestCase("settings", "Get Session Settings", "GET", "/api/admin/system/settings/session", expected_status=200),
        TestCase("settings", "Get Node Settings", "GET", "/api/admin/system/settings/node", expected_status=200),
        TestCase("settings", "Get Security Settings", "GET", "/api/admin/system/settings/security", expected_status=200),
        TestCase("settings", "Get Storage Settings", "GET", "/api/admin/system/settings/storage", expected_status=200),
        TestCase("settings", "Get Execution Settings", "GET", "/api/admin/system/settings/execution", expected_status=200),
        TestCase("settings", "Get Ticket Settings", "GET", "/api/admin/system/settings/ticket", expected_status=200),
        TestCase("settings", "Get User Settings", "GET", "/api/admin/system/settings/user", expected_status=200),
        TestCase("settings", "Get Role Settings", "GET", "/api/admin/system/settings/role", expected_status=200),
    ]
    suites.append(("Runtime System Settings (16-settings / 16-settings-systemadmin)", settings_cases))

    # 11. Audit & License Domain (17-audit, 13-licenses)
    audit_license_cases = [
        TestCase("audit", "Admin Tail Audit Log", "GET", "/api/admin/audit/logs/audit?tail=10", expected_status=200),
        TestCase("audit", "Admin List Launcher Executions", "GET", "/api/admin/audit/executions", expected_status=200),
        TestCase("licenses", "User List License Services", "GET", "/api/licenses", expected_status=200),
        TestCase("licenses", "Admin List License Services", "GET", "/api/admin/system/licenses", expected_status=200),
    ]
    suites.append(("Audit & License Domain (17-audit / 13-licenses)", audit_license_cases))

    # 12. Tickets & Projects (21-tickets, 21-tickets-systemadmin)
    ticket_cases = [
        TestCase("tickets", "User List Tickets", "GET", "/api/tickets", expected_status=200),
        TestCase("tickets", "Admin List Projects", "GET", "/api/admin/system/tickets/projects", expected_status=200),
        TestCase("tickets", "Admin List All Tickets", "GET", "/api/admin/system/tickets", expected_status=200),
    ]
    suites.append(("Tickets & Projects (21-tickets / 21-tickets-systemadmin)", ticket_cases))

    # 13. Global Dashboard (18-dashboard.http)
    dashboard_cases = [
        TestCase("dashboard", "Get Live System Dashboard State", "GET", "/api/dashboard", expected_status=200),
    ]
    suites.append(("Dashboard Domain (18-dashboard.http)", dashboard_cases))

    # 14. RBAC Scope & Privilege Boundary Isolation
    user_tok_holder = [None]
    def capture_user_token(d):
        tok = d.get("data", {}).get("token")
        if tok:
            user_tok_holder[0] = tok
            return True, ""
        return False, "Failed to capture normal user token"

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
        ),
        TestCase(
            "rbac",
            "Normal User Access Admin Settings (Forbidden)",
            "GET",
            "/api/admin/system/settings/platform",
            token=lambda: user_tok_holder[0],
            expected_status=403,
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

def get_available_domains(suites: List[Tuple[str, List[TestCase]]]) -> List[str]:
    seen = set()
    result = []
    for _, cases in suites:
        for c in cases:
            if c.domain not in seen:
                seen.add(c.domain)
                result.append(c.domain)
    return result

def main():
    parser = argparse.ArgumentParser(description="Design Cloud Engine Portal HTTP API Test Runner")
    parser.add_argument("--env", default="dev-centvm-ldap-hpcadmin", help="Environment name from http-client.env.json (default: dev-centvm-ldap-hpcadmin)")
    parser.add_argument("--addr", help="Override base URL (e.g. http://10.1.1.100:1234)")
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

    args = parser.parse_args()

    if args.no_color or not sys.stdout.isatty():
        Colors.disable()

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

    runner = TestRunner(base_url, timeout=args.timeout, verbose=args.verbose)
    suites = build_test_suite(runner, admin_user, admin_pwd, normal_user, normal_pwd, test_node)

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
    print(f"{Colors.BOLD}================================================================================{Colors.RESET}\n")

    # Fast connectivity check
    print(f"[*] Probing connectivity to {base_url} ...", end=" ", flush=True)
    is_up, probe_err = runner.check_connectivity()
    if not is_up:
        print(f"{Colors.RED}UNREACHABLE{Colors.RESET}")
        print(f"\n{Colors.RED}Connection Failure:{Colors.RESET} Cannot connect to server at {base_url}")
        print(f"  Details: {probe_err}")
        print(f"  Please ensure Design Cloud Engine server is running on {base_url} or specify --addr.\n")
        sys.exit(1)
    print(f"{Colors.GREEN}ONLINE{Colors.RESET}")

    for suite_name, cases in suites:
        filtered_cases = [c for c in cases if not args.domain or c.domain == args.domain]
        if not filtered_cases:
            continue

        print(f"\n{Colors.BOLD}>>> {suite_name}{Colors.RESET}")
        for tc in filtered_cases:
            if tc.domain not in ["public", "auth_guard"] and not runner.token:
                runner.ensure_login(admin_user, admin_pwd)
            passed = runner.run(tc)
            if not passed and args.fail_fast:
                print(f"\n{Colors.RED}[FAIL-FAST] Terminating suite early due to test failure.{Colors.RESET}")
                break
        if args.fail_fast and any(not r["passed"] for r in runner.results):
            break

    total = len(runner.results)
    passed = sum(1 for r in runner.results if r["passed"])
    failed = total - passed
    avg_dur = sum(r["duration"] for r in runner.results) / total if total else 0

    print(f"\n{Colors.BOLD}================================================================================{Colors.RESET}")
    status_color = Colors.GREEN if failed == 0 else Colors.RED
    print(f"  {status_color}Test Results: {passed}/{total} Passed (Failed: {failed}) | Avg Latency: {avg_dur:.1f}ms{Colors.RESET}")
    print(f"{Colors.BOLD}================================================================================{Colors.RESET}\n")

    if args.json:
        print(json.dumps(runner.results, indent=2))

    if failed > 0:
        print(f"{Colors.RED}Failed Test Cases:{Colors.RESET}")
        for r in runner.results:
            if not r["passed"]:
                print(f"  - [{r['method']} {r['path']}] Expected {r['expected']}, got {r['actual']}: {r.get('msg')}")
        sys.exit(1)
    else:
        print(f"{Colors.GREEN}All tested Portal API endpoints succeeded!{Colors.RESET}\n")

if __name__ == "__main__":
    main()
