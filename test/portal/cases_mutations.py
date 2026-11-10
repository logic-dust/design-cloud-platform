"""Stateful Portal API flows: create → exercise → cleanup (files, tickets, licenses admin)."""

from __future__ import annotations

import time
from typing import Any, Dict, List, Tuple

from run_http_tests import TestCase, TestRunner


def build_mutation_suites(runner: TestRunner) -> List[Tuple[str, List[TestCase]]]:
    stamp = int(time.time())
    probe_file = f"api_runner_probe_{stamp}.txt"
    lic_name = f"api-runner-lic-{stamp}"
    project_name = f"api-runner-project-{stamp}"

    state: Dict[str, Any] = {
        "user_id": None,
        "project_id": None,
        "ticket_id": None,
        "ticket_version": None,
        "license_id": None,
        "files_enabled": True,
    }

    def skip_files_disabled() -> bool:
        return not state["files_enabled"]

    def capture_user_id(d: Dict[str, Any]) -> Tuple[bool, str]:
        data = d.get("data") or {}
        uid = data.get("id") or data.get("userId")
        if uid is not None:
            state["user_id"] = int(uid)
        return True, ""

    def capture_project_id(d: Dict[str, Any]) -> Tuple[bool, str]:
        items = d.get("data")
        if isinstance(items, list) and items:
            pid = items[0].get("id")
            if pid is not None:
                state["project_id"] = int(pid)
        return True, ""

    def capture_created_project(d: Dict[str, Any]) -> Tuple[bool, str]:
        pid = (d.get("data") or {}).get("id")
        if pid is not None:
            state["project_id"] = int(pid)
        return True, ""

    def capture_ticket_id(d: Dict[str, Any]) -> Tuple[bool, str]:
        data = d.get("data") or {}
        if data.get("id") is not None:
            state["ticket_id"] = int(data["id"])
        if data.get("version") is not None:
            state["ticket_version"] = int(data["version"])
        return True, ""

    def capture_license_id(d: Dict[str, Any]) -> Tuple[bool, str]:
        lid = (d.get("data") or {}).get("id")
        if lid is not None:
            state["license_id"] = int(lid)
        return True, ""

    def probe_files_gate(d: Dict[str, Any]) -> Tuple[bool, str]:
        if d.get("data") is None and d.get("message"):
            state["files_enabled"] = False
        return True, ""

    def ticket_create_body() -> Dict[str, Any]:
        return {
            "projectId": state["project_id"] or 1,
            "type": "task",
            "title": f"API runner ticket {stamp}",
            "description": "automated mutation flow",
            "priority": "medium",
            "status": "todo",
        }

    def batch_delete_ticket_body() -> Dict[str, Any]:
        return {"ids": [state["ticket_id"]]}

    suites: List[Tuple[str, List[TestCase]]] = []

    suites.append(
        (
            "Files write flow (10-files.http)",
            [
                TestCase(
                    "files",
                    "Probe File Feature Enabled",
                    "GET",
                    "/api/files/locations",
                    expected_status=[200, 403],
                    validator=probe_files_gate,
                ),
                TestCase(
                    "files",
                    f"Upload Probe File ({probe_file})",
                    "PUT",
                    f"/api/files/locations/home/contents/{probe_file}",
                    raw_body="design-cloud-api-runner\n",
                    content_type="text/plain",
                    expected_status=[200, 201, 204],
                    skip_if=skip_files_disabled,
                ),
                TestCase(
                    "files",
                    "Direct Download Stream",
                    "POST",
                    "/api/files/downloads",
                    body={"location": "home", "path": probe_file},
                    expected_status=[200, 403, 404],
                    skip_if=skip_files_disabled,
                ),
                TestCase(
                    "files",
                    "Create Download Request",
                    "POST",
                    "/api/files/download-requests",
                    body={"location": "home", "path": probe_file},
                    expected_status=[202, 409],
                    strict_expected=[202, 409],
                    skip_if=skip_files_disabled,
                ),
                TestCase(
                    "files",
                    "Permanent Delete Probe File",
                    "POST",
                    "/api/files/locations/home/contents/batch-delete",
                    body={"paths": [probe_file]},
                    expected_status=[200, 204, 404],
                    skip_if=skip_files_disabled,
                ),
            ],
        )
    )

    suites.append(
        (
            "Tickets CRUD flow (21-tickets.http)",
            [
                TestCase(
                    "tickets",
                    "Capture Current User Id",
                    "GET",
                    "/api/users/me",
                    expected_status=200,
                    validator=capture_user_id,
                ),
                TestCase(
                    "tickets",
                    "Resolve Project Id From List",
                    "GET",
                    "/api/tickets/projects",
                    expected_status=200,
                    validator=capture_project_id,
                ),
                TestCase(
                    "tickets",
                    "Create Project If Missing",
                    "POST",
                    "/api/admin/system/tickets/projects",
                    body={"name": project_name, "description": "api runner ephemeral project"},
                    expected_status=[200, 201, 409],
                    validator=capture_created_project,
                    skip_if=lambda: state["project_id"] is not None,
                ),
                TestCase(
                    "tickets",
                    "Create Ticket",
                    "POST",
                    "/api/tickets",
                    body_factory=ticket_create_body,
                    expected_status=201,
                    validator=capture_ticket_id,
                ),
                TestCase(
                    "tickets",
                    "Get Created Ticket",
                    "GET",
                    "/api/tickets/0",
                    path_factory=lambda: f"/api/tickets/{state['ticket_id']}",
                    expected_status=200,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
                TestCase(
                    "tickets",
                    "Patch Ticket Status",
                    "PATCH",
                    "/api/tickets/0",
                    path_factory=lambda: f"/api/tickets/{state['ticket_id']}",
                    body_factory=lambda: {"status": "inProgress", "version": state["ticket_version"]},
                    expected_status=200,
                    validator=capture_ticket_id,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
                TestCase(
                    "tickets",
                    "Move Ticket To Column End",
                    "POST",
                    "/api/tickets/0/move",
                    path_factory=lambda: f"/api/tickets/{state['ticket_id']}/move",
                    body_factory=lambda: {"status": "inReview", "version": state["ticket_version"]},
                    expected_status=200,
                    validator=capture_ticket_id,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
                TestCase(
                    "tickets",
                    "Stale Version Conflict",
                    "PATCH",
                    "/api/tickets/0",
                    path_factory=lambda: f"/api/tickets/{state['ticket_id']}",
                    body={"title": "stale", "version": 1},
                    expected_status=409,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
                TestCase(
                    "tickets",
                    "Comment On Ticket",
                    "POST",
                    "/api/tickets/0/comments",
                    path_factory=lambda: f"/api/tickets/{state['ticket_id']}/comments",
                    body={"body": "api runner comment"},
                    expected_status=201,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
                TestCase(
                    "tickets",
                    "List Ticket Events",
                    "GET",
                    "/api/tickets/0/events",
                    path_factory=lambda: f"/api/tickets/{state['ticket_id']}/events",
                    expected_status=200,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
                TestCase(
                    "tickets",
                    "Batch Delete Created Ticket",
                    "POST",
                    "/api/tickets/batch-delete",
                    body_factory=batch_delete_ticket_body,
                    expected_status=204,
                    skip_if=lambda: state["ticket_id"] is None,
                ),
            ],
        )
    )

    def validate_platform_name_schema(d: Dict[str, Any]) -> Tuple[bool, str]:
        props = ((d.get("data") or {}).get("properties") or {})
        name = props.get("name") or {}
        if name.get("minLength") != 1 or name.get("maxLength") != 64:
            return False, f"platform.name length bounds: {name!r}"
        return True, ""

    suites.append(
        (
            "Settings string constraints (16-settings-system-admin.http)",
            [
                TestCase(
                    "settings",
                    "Platform schema name minLength/maxLength",
                    "GET",
                    "/api/admin/system/settings/platform/schema",
                    expected_status=200,
                    validator=validate_platform_name_schema,
                ),
                TestCase(
                    "settings",
                    "Platform name exceeds maxLength",
                    "PUT",
                    "/api/admin/system/settings/platform",
                    body={"name": "x" * 65},
                    expected_status=422,
                ),
            ],
        )
    )

    suites.append(
        (
            "Licenses admin flow (13-licenses-system-admin.http)",
            [
                TestCase(
                    "licenses",
                    "Admin Create License Service",
                    "POST",
                    "/api/admin/system/licenses",
                    body={
                        "name": lic_name,
                        "type": "flexlm",
                        "address": "27000@127.0.0.1",
                        "comment": "api runner ephemeral license",
                        "enabled": True,
                    },
                    expected_status=201,
                    validator=capture_license_id,
                ),
                TestCase(
                    "licenses",
                    "Admin Get License Service",
                    "GET",
                    "/api/admin/system/licenses/0",
                    path_factory=lambda: f"/api/admin/system/licenses/{state['license_id']}",
                    expected_status=200,
                    skip_if=lambda: state["license_id"] is None,
                ),
                TestCase(
                    "licenses",
                    "Admin Update License Service",
                    "PUT",
                    "/api/admin/system/licenses/0",
                    path_factory=lambda: f"/api/admin/system/licenses/{state['license_id']}",
                    body={
                        "name": lic_name + "-updated",
                        "type": "flexlm",
                        "address": "27001@127.0.0.1",
                        "comment": "updated by api runner",
                        "enabled": True,
                    },
                    expected_status=200,
                    skip_if=lambda: state["license_id"] is None,
                ),
                TestCase(
                    "licenses",
                    "Admin Delete License Service",
                    "DELETE",
                    "/api/admin/system/licenses/0",
                    path_factory=lambda: f"/api/admin/system/licenses/{state['license_id']}",
                    expected_status=204,
                    skip_if=lambda: state["license_id"] is None,
                ),
                TestCase(
                    "licenses",
                    "Admin Create License Invalid Type",
                    "POST",
                    "/api/admin/system/licenses",
                    body={
                        "name": f"bad-lic-{stamp}",
                        "type": "not-a-real-type",
                        "address": "27000@127.0.0.1",
                    },
                    expected_status=422,
                ),
            ],
        )
    )

    return suites
