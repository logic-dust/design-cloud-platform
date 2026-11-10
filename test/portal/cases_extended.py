"""Additional Portal API TestCase suites aligned with test/portal/*.http (read-heavy + safe actions)."""

from __future__ import annotations

from typing import Any, Callable, Dict, List, Optional, Tuple

from run_http_tests import TestCase

_FIXED_TEST_VNC_RESOURCE_NAME = "fixed-test-vnc-resource"


def _rows(data: Dict[str, Any]) -> list:
    items = data.get("data")
    return items if isinstance(items, list) else []


def _capture_license_id(state: Dict[str, Any], d: Dict[str, Any]) -> Tuple[bool, str]:
    items = d.get("data")
    if isinstance(items, list) and items:
        lid = items[0].get("id")
        if lid is not None:
            state["license_id"] = str(lid)
    return True, ""


def _capture_connection_ids(state: Dict[str, Any], d: Dict[str, Any]) -> Tuple[bool, str]:
    items = d.get("data")
    if isinstance(items, list) and items:
        cid = items[0].get("id")
        if cid is not None:
            state["connection_id"] = str(cid)
    return True, ""


def _capture_resource_id(state: Dict[str, Any], d: Dict[str, Any]) -> Tuple[bool, str]:
    for row in _rows(d):
        if row.get("name") == _FIXED_TEST_VNC_RESOURCE_NAME and row.get("id") is not None:
            state["resource_id"] = str(row["id"])
            return True, ""
    return True, ""


def _capture_job_id(state: Dict[str, Any], d: Dict[str, Any]) -> Tuple[bool, str]:
    data = d.get("data") or {}
    jobs = data.get("jobs") or data.get("Jobs") or []
    if isinstance(jobs, list) and jobs:
        job = jobs[0]
        jid = job.get("JOBID") or job.get("job_id") or job.get("jobId") or job.get("id")
        if jid is not None:
            state["job_id"] = str(jid)
    return True, ""


def _validate_system_status(d: Dict[str, Any]) -> Tuple[bool, str]:
    data = d.get("data") if isinstance(d, dict) else None
    if not isinstance(data, dict):
        return False, "expected data object in response"
    for key in ("platform", "auth", "database", "portal", "nodes", "log"):
        if key not in data:
            return False, f"missing expected field: {key}"
    plat = data.get("platform")
    if not isinstance(plat, dict) or "version" not in plat or "servers" not in plat:
        return False, "platform field invalid or missing version/servers"
    auth = data.get("auth")
    if not isinstance(auth, dict) or "authMode" not in auth or "threeRoles" not in auth:
        return False, "auth field invalid or missing authMode/threeRoles"
    return True, ""


def build_extended_suites(
    runner: TestRunner,
    test_node: str,
    test_app_id: str,
    test_job_id: str,
    test_group: str,
    normal_user: str,
) -> List[Tuple[str, List[TestCase]]]:
    state: Dict[str, Any] = {
        "license_id": None,
        "connection_id": None,
        "resource_id": None,
        "job_id": test_job_id,
    }

    def conn_path(suffix: str) -> str:
        cid = state["connection_id"] or "1"
        return f"/api/connections/{cid}{suffix}"

    def license_path(suffix: str) -> str:
        lid = state["license_id"] or "1"
        return f"/api/licenses/{lid}{suffix}"

    suites: List[Tuple[str, List[TestCase]]] = []

    suites.append(
        (
            "Public APIs extended (01-public-api.http)",
            [
                TestCase("public", "Desktop Latest (windows query)", "GET", "/api/desktop/releases/latest?os=windows", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Latest (limit=1)", "GET", "/api/desktop/releases/latest?limit=1", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Installer metadata", "GET", "/api/desktop/releases/installer", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Bootstrap Installer metadata", "GET", "/api/desktop/releases/bootstrap-installer", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Download Latest", "GET", "/api/desktop/releases/download/latest", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Download Latest (linux)", "GET", "/api/desktop/releases/download/latest?os=linux", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase(
                    "public",
                    "Desktop Download Named Package",
                    "GET",
                    "/api/desktop/releases/download/DesignCloudDesktop-1.0.0.nupkg",
                    token=None,
                    expected_status=[200, 401, 404],
                    probe=True,
                ),
                TestCase("public", "Desktop Plugin Latest", "GET", "/api/desktop/plugins/turbovnc/latest", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Plugin Download Latest", "GET", "/api/desktop/plugins/turbovnc/download/latest", token=None, expected_status=[200, 401, 404], probe=True),
                TestCase("public", "Desktop Plugin Download File", "GET", "/api/desktop/plugins/turbovnc/download/latest.zip", token=None, expected_status=[200, 401, 404], probe=True),
            ],
        )
    )

    suites.append(
        (
            "Session extended (02-session.http)",
            [
                TestCase("session", "List My Sessions", "GET", "/api/sessions", expected_status=200),
                TestCase(
                    "session",
                    "Delete Nonexistent Session",
                    "DELETE",
                    "/api/sessions/999999999",
                    expected_status=[204, 404, 403],
                    strict_expected=[204, 404, 403],
                ),
                TestCase(
                    "session",
                    "Batch Delete Sessions (empty ids rejected)",
                    "POST",
                    "/api/sessions/batch-delete",
                    body={"ids": []},
                    expected_status=400,
                ),
            ],
        )
    )

    suites.append(
        (
            "Security Admin extended (03-05)",
            [
                TestCase(
                    "security",
                    "List Superadmin Role Members",
                    "GET",
                    "/api/admin/security/roles/superadmin/members?limit=50&offset=0",
                    expected_status=[200, 403, 404],
                ),
                TestCase(
                    "security",
                    "Pending File Download Requests",
                    "GET",
                    "/api/admin/security/files/download-requests?status=pending",
                    expected_status=200,
                ),
                TestCase(
                    "security",
                    "Get File Download Request By Id",
                    "GET",
                    "/api/admin/security/files/download-requests/1",
                    expected_status=[200, 404],
                    probe=True,
                ),
            ],
        )
    )

    suites.append(
        (
            "Applications extended (06-apps)",
            [
                TestCase("apps", f"User Get App ({test_app_id})", "GET", f"/api/apps/{test_app_id}", expected_status=[200, 404], probe=True),
                TestCase(
                    "apps",
                    f"User Launch App ({test_app_id})",
                    "POST",
                    f"/api/apps/{test_app_id}/launch",
                    body={},
                    expected_status=[202, 404, 409],
                    strict_expected=[202, 409],
                    probe=True,
                ),
                TestCase("apps", f"Admin Get App ({test_app_id})", "GET", f"/api/admin/system/apps/{test_app_id}", expected_status=[200, 404], probe=True),
                TestCase(
                    "apps",
                    f"Admin List App Users ({test_app_id})",
                    "GET",
                    f"/api/admin/system/apps/{test_app_id}/users",
                    expected_status=[200, 404],
                    probe=True,
                ),
            ],
        )
    )

    suites.append(
        (
            "Scheduler lists (07-scheduler)",
            [
                TestCase("scheduler", "List Hosts", "GET", "/api/scheduler/hosts", expected_status=200),
                TestCase("scheduler", "List Queues", "GET", "/api/scheduler/queues", expected_status=200),
                TestCase("scheduler", "List Cluster Jobs", "GET", "/api/scheduler/jobs", expected_status=200),
                TestCase(
                    "scheduler",
                    "List My Jobs",
                    "GET",
                    "/api/scheduler/jobs/me",
                    expected_status=200,
                    validator=lambda d: _capture_job_id(state, d),
                ),
                TestCase("scheduler", "Admin List Hosts", "GET", "/api/admin/system/scheduler/hosts", expected_status=200),
                TestCase("scheduler", "Admin List Queues", "GET", "/api/admin/system/scheduler/queues", expected_status=200),
                TestCase(
                    "scheduler",
                    "Admin List Cluster Jobs",
                    "GET",
                    "/api/admin/system/scheduler/jobs",
                    expected_status=200,
                ),
            ],
        )
    )

    suites.append(
        (
            "Connections extended (08-connections)",
            [
                TestCase(
                    "connections",
                    "User List Connection Resources",
                    "GET",
                    "/api/connections/resources",
                    expected_status=200,
                    validator=lambda d: _capture_resource_id(state, d),
                ),
                TestCase(
                    "connections",
                    "User Get Connection Resource",
                    "GET",
                    f"/api/connections/resources/{state.get('resource_id') or 1}",
                    expected_status=[200, 404],
                    probe=True,
                ),
                TestCase(
                    "connections",
                    "Capture Connection Id",
                    "GET",
                    "/api/connections",
                    expected_status=200,
                    validator=lambda d: _capture_connection_ids(state, d),
                ),
                TestCase("connections", "Get Connection Detail", "GET", conn_path(""), expected_status=[200, 404], probe=True),
                TestCase("connections", "Touch Connection", "POST", conn_path("/touch"), expected_status=[204, 404, 403], probe=True),
                TestCase("connections", "User Refresh Connections", "POST", "/api/connections/refresh", expected_status=[200, 202, 204]),
                TestCase(
                    "connections",
                    "VNC OTP For Connection",
                    "POST",
                    conn_path("/vnc-otp"),
                    expected_status=[200, 404, 403],
                    probe=True,
                ),
                TestCase(
                    "connections",
                    "Start Connection (op)",
                    "POST",
                    conn_path("/start"),
                    expected_status=[202, 404, 400, 403],
                    probe=True,
                ),
                TestCase(
                    "connections",
                    "Stop Connection (op)",
                    "POST",
                    conn_path("/stop"),
                    expected_status=[202, 404, 400, 403],
                    probe=True,
                ),
                TestCase("connections", "Admin List Connection Resources", "GET", "/api/admin/system/connections/resources", expected_status=200),
                TestCase(
                    "connections",
                    "Admin Get Connection Resource",
                    "GET",
                    f"/api/admin/system/connections/resources/{state.get('resource_id') or 1}",
                    expected_status=[200, 404],
                    probe=True,
                ),
                TestCase(
                    "connections",
                    "Admin Connections Cleanup",
                    "POST",
                    "/api/admin/system/connections/cleanup",
                    expected_status=[200, 202, 204],
                ),
            ],
        )
    )

    suites.append(
        (
            "Files extended (10-files)",
            [
                TestCase("files", "List File Locations", "GET", "/api/files/locations", expected_status=[200, 403]),
                TestCase("files", "List Home Contents", "GET", "/api/files/locations/home/contents", expected_status=[200, 403, 404]),
                TestCase("files", "List Home .config", "GET", "/api/files/locations/home/contents/.config", expected_status=[200, 403, 404]),
                TestCase(
                    "files",
                    "List Public Contents",
                    "GET",
                    "/api/files/locations/public/contents",
                    expected_status=[200, 403, 404],
                    strict_expected=[200, 403, 404],
                ),
                TestCase("files", "Get Download Request By Id", "GET", "/api/files/download-requests/1", expected_status=[200, 404, 403], probe=True),
                TestCase("files", "List Trash Contents", "GET", "/api/files/trash/contents", expected_status=[200, 403, 404]),
            ],
        )
    )

    suites.append(
        (
            "Licenses extended (13-licenses)",
            [
                TestCase(
                    "licenses",
                    "Capture License Id",
                    "GET",
                    "/api/licenses",
                    expected_status=200,
                    validator=lambda d: _capture_license_id(state, d),
                ),
                TestCase("licenses", "Get License Detail", "GET", license_path(""), expected_status=[200, 404], probe=True),
                TestCase("licenses", "Get License Features", "GET", license_path("/features"), expected_status=[200, 404], probe=True),
                TestCase("licenses", "Get License Status", "GET", license_path("/status"), expected_status=[200, 404], probe=True),
            ],
        )
    )

    suites.append(
        (
            "Tickets extended (21-tickets)",
            [
                TestCase("tickets", "List Ticket Projects (user)", "GET", "/api/tickets/projects", expected_status=200),
                TestCase("tickets", "Ticket Summary", "GET", "/api/tickets/summary", expected_status=200),
                TestCase("tickets", "Ticket Assignees Search", "GET", "/api/tickets/assignees?q=te", expected_status=200),
                TestCase("tickets", "Ticket Assignees Hot List (short q)", "GET", "/api/tickets/assignees?q=a", expected_status=200),
                TestCase("tickets", "Get Ticket By Id", "GET", "/api/tickets/1", expected_status=[200, 404], probe=True),
                TestCase("tickets", "List Tickets Multi Status", "GET", "/api/tickets?status=todo,inProgress&sort=updatedAt", expected_status=200),
                TestCase("tickets", "List Tickets Invalid Status", "GET", "/api/tickets?status=0", expected_status=422),
                TestCase("tickets", "List Tickets Invalid Limit", "GET", "/api/tickets?limit=0", expected_status=422),
                TestCase("tickets", "Ticket Summary With Tz", "GET", "/api/tickets/summary?days=30&tz=Asia/Shanghai", expected_status=200),
                TestCase("tickets", "Ticket Summary Invalid Tz", "GET", "/api/tickets/summary?tz=Mars/Base", expected_status=422),
                TestCase("tickets", "Delete Missing Ticket", "DELETE", "/api/tickets/999999999", expected_status=404),
            ],
        )
    )

    suites.append(
        (
            "Nodes extended (12-nodes-system-admin.http)",
            [
                TestCase("nodes", f"Get Agent Config ({test_node})", "GET", f"/api/admin/system/nodes/{test_node}/config", expected_status=200),
                TestCase(
                    "nodes",
                    "Batch Update Node Settings (noop body)",
                    "POST",
                    "/api/admin/system/nodes/settings/batch-update",
                    body={"updates": []},
                    expected_status=[200, 400, 422],
                ),
            ],
        )
    )

    suites.append(
        (
            "Status, Storage & Alerts (19-status / alerts)",
            [
                TestCase(
                    "status",
                    "Get System Status",
                    "GET",
                    "/api/admin/system/status",
                    expected_status=200,
                    validator=_validate_system_status,
                ),
                TestCase("storage", "Refresh Storage Snapshot", "POST", "/api/admin/system/storage/refresh", expected_status=[200, 202, 204]),
                TestCase(
                    "storage",
                    "Storage Share History",
                    "GET",
                    "/api/admin/system/storage/share/history?days=1",
                    expected_status=[200, 404],
                    probe=True,
                ),
                TestCase("alerts", "Alert History", "GET", "/api/admin/system/alerts/history?limit=10", expected_status=200),
            ],
        )
    )

    suites.append(
        (
            "Audit extended (17-audit)",
            [
                TestCase("audit", "Tail Security Log", "GET", "/api/admin/audit/logs/security?tail=5", expected_status=[200, 404], probe=True),
                TestCase("audit", "Get Execution Detail", "GET", "/api/admin/audit/executions/nonexistent-id", expected_status=[404, 400]),
            ],
        )
    )

    return suites

