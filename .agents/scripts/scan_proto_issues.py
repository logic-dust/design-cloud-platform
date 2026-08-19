#!/usr/bin/env python3
"""
scan_proto_issues.py - Protobuf Baggage & Health Analyzer
Scans internal/contract/proto/*.proto files for reserved fields, oneof naming inconsistencies,
field number gaps, and field set redundancies.
"""

import os
import re
import sys

PROTO_DIR = "internal/contract/proto"

def scan_proto_files():
    if not os.path.exists(PROTO_DIR):
        print(f"Directory {PROTO_DIR} not found.")
        sys.exit(1)

    proto_files = [os.path.join(PROTO_DIR, f) for f in os.listdir(PROTO_DIR) if f.endswith(".proto")]
    if not proto_files:
        print(f"No .proto files found in {PROTO_DIR}.")
        sys.exit(0)

    print("========================================================================")
    print("  Protobuf Baggage & Structural Health Scan Report")
    print("========================================================================")

    total_issues = 0
    for filepath in sorted(proto_files):
        filename = os.path.basename(filepath)
        print(f"\n📄 Analyzing file: {filename}")
        with open(filepath, "r", encoding="utf-8") as f:
            content = f.read()

        issues = 0
        issues += analyze_reserved(filename, content)
        issues += analyze_oneof(filename, content)
        issues += analyze_field_gaps(filename, content)
        issues += analyze_report_timestamps(filename, content)
        issues += analyze_section_placement(filename, content)
        total_issues += issues

    if total_issues > 0:
        print(f"\n❌ Protobuf health scan failed with {total_issues} issue(s).")
        sys.exit(1)
    else:
        print("\n✅ All Protobuf files are clean, compliant, and well-structured.")
        sys.exit(0)

def analyze_reserved(filename, content):
    lines = content.splitlines()
    reserved_found = []
    for idx, line in enumerate(lines, 1):
        if "reserved" in line:
            has_name = '"' in line or "'" in line
            risk = "🔴 High Risk (named reservation)" if has_name else "🟢 Low Risk (unnamed index reservation)"
            reserved_found.append((idx, line.strip(), risk))

    if reserved_found:
        print("  📌 Reserved Fields Found:")
        for line_num, code, risk in reserved_found:
            print(f"     L{line_num}: {code}  ->  [{risk}]")
        return len(reserved_found)
    else:
        print("  ✅ Reserved Fields: Clean")
        return 0

def analyze_oneof(filename, content):
    oneof_pattern = re.compile(r'oneof\s+(\w+)\s*\{([^}]+)\}', re.MULTILINE)
    mismatches = []
    for match in oneof_pattern.finditer(content):
        oneof_name = match.group(1)
        body = match.group(2)
        field_lines = [l.strip() for l in body.splitlines() if l.strip() and not l.strip().startswith("//")]
        for f in field_lines:
            parts = f.split()
            if len(parts) >= 2:
                field_type = parts[0]
                field_name = parts[1]
                # convert PascalCase Type to snake_case (abbreviation-aware)
                s1 = re.sub(r'(.)([A-Z][a-z]+)', r'\1_\2', field_type)
                expected_name = re.sub(r'([a-z0-9])([A-Z])', r'\1_\2', s1).lower()
                if field_name != expected_name and field_name != field_type:
                    mismatches.append((oneof_name, field_name, field_type, expected_name))

    if mismatches:
        print("  📌 Oneof Naming Inconsistencies Found:")
        for oneof_name, field_name, field_type, expected in mismatches:
            print(f"     oneof '{oneof_name}': field '{field_name}' vs type '{field_type}' -> Recommended: '{expected}'")
        return len(mismatches)
    else:
        print("  ✅ Oneof Naming: Compliant")
        return 0

def analyze_field_gaps(filename, content):
    message_pattern = re.compile(r'message\s+(\w+)\s*\{([^}]+)\}', re.MULTILINE)
    gaps_found = []
    for match in message_pattern.finditer(content):
        msg_name = match.group(1)
        body = match.group(2)
        numbers = []
        for line in body.splitlines():
            line = line.strip()
            if not line or line.startswith("//") or line.startswith("reserved") or line.startswith("oneof"):
                continue
            num_match = re.search(r'=\s*(\d+)\s*;', line)
            if num_match:
                numbers.append(int(num_match.group(1)))

        numbers.sort()
        if numbers:
            expected = 1
            missing = []
            for n in numbers:
                while expected < n:
                    missing.append(expected)
                    expected += 1
                expected = n + 1
            if missing:
                gaps_found.append((msg_name, numbers, missing))

    if gaps_found:
        print("  📌 Field Number Gaps Found:")
        for msg_name, numbers, missing in gaps_found:
            print(f"     message '{msg_name}': present={numbers}, missing={missing}")
        return len(gaps_found)
    else:
        print("  ✅ Field Number Continuity: Continuous")
        return 0

def analyze_report_timestamps(filename, content):
    msg_pattern = re.compile(r'message\s+(\w+)\s*\{([^}]+)\}', re.MULTILINE)
    issues = 0
    for match in msg_pattern.finditer(content):
        msg_name = match.group(1)
        body = match.group(2)
        if msg_name.endswith("Report") or msg_name == "HeartbeatRequest":
            for line in body.splitlines():
                line = line.strip()
                if re.search(r'\bint64\s+timestamp\b', line):
                    print(f"  📌 Timestamp Naming Issue: message '{msg_name}' has field 'timestamp', expected 'reported_at'")
                    issues += 1
        elif msg_name == "RegisterRequest":
            for line in body.splitlines():
                line = line.strip()
                if re.search(r'\bint64\s+timestamp\b', line):
                    print(f"  📌 Timestamp Naming Issue: message '{msg_name}' has field 'timestamp', expected 'registered_at'")
                    issues += 1
    if issues == 0:
        print("  ✅ Report Timestamps: Compliant (using 'reported_at' / 'registered_at')")
    return issues

def analyze_section_placement(filename, content):
    server_cmd_match = re.search(r'message\s+ServerCommand\s*\{([^}]+)\}', content)
    agent_msg_match = re.search(r'message\s+AgentMessage\s*\{([^}]+)\}', content)
    server_marker = re.search(r'// =+ Server → Agent', content)
    if not (server_cmd_match and agent_msg_match and server_marker):
        return 0

    server_marker_pos = server_marker.start()
    server_payload_types = []
    for line in server_cmd_match.group(1).splitlines():
        line = line.strip()
        parts = line.split()
        if len(parts) >= 2 and not line.startswith("//"):
            server_payload_types.append(parts[0])

    agent_payload_types = []
    for line in agent_msg_match.group(1).splitlines():
        line = line.strip()
        parts = line.split()
        if len(parts) >= 2 and not line.startswith("//"):
            agent_payload_types.append(parts[0])

    issues = 0
    for payload_type in server_payload_types:
        def_match = re.search(r'\bmessage\s+' + re.escape(payload_type) + r'\b', content)
        if def_match and def_match.start() < server_marker_pos:
            print(f"  📌 Section Misplacement: ServerCommand payload '{payload_type}' defined in Agent section")
            issues += 1

    for payload_type in agent_payload_types:
        def_match = re.search(r'\bmessage\s+' + re.escape(payload_type) + r'\b', content)
        if def_match and def_match.start() > server_marker_pos:
            print(f"  📌 Section Misplacement: AgentMessage payload '{payload_type}' defined in Server section")
            issues += 1

    if issues == 0:
        print("  ✅ Section Placement: Clean (Bidirectional payload alignment compliant)")
    return issues

if __name__ == "__main__":
    scan_proto_files()
