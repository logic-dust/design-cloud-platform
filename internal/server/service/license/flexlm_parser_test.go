package license

import (
	"testing"
)

func TestParseFlexFeatureOverviewLine(t *testing.T) {
	// 1. Success usage line
	line := "Users of calibreXRC:  (Total of 5 licenses issued;  Total of 2 licenses in use)"
	info := parseFlexFeatureOverviewLine(line)
	if info.Name != "calibreXRC" {
		t.Errorf("expected name calibreXRC, got %s", info.Name)
	}
	if info.Total != 5 {
		t.Errorf("expected total 5, got %d", info.Total)
	}
	if info.Used != 2 {
		t.Errorf("expected used 2, got %d", info.Used)
	}

	// 2. Error line
	errLine := "Users of b_pw_core:  (Error: 1 licences, unsupported by licensed server)"
	infoErr := parseFlexFeatureOverviewLine(errLine)
	if infoErr.Name != "b_pw_core" {
		t.Errorf("expected name b_pw_core on error line, got %s", infoErr.Name)
	}
	if infoErr.Total != -1 || infoErr.Used != -1 {
		t.Errorf("expected total and used to be -1 on error, got total=%d, used=%d", infoErr.Total, infoErr.Used)
	}

	// 3. No match line
	noMatch := "Invalid line here"
	infoNoMatch := parseFlexFeatureOverviewLine(noMatch)
	if infoNoMatch.Name != "" {
		t.Errorf("expected empty name, got %s", infoNoMatch.Name)
	}
}

func TestParseFlexFeatureUsedDetailLine(t *testing.T) {
	// 1. user
	userLine := "user1 cn07 /dev/pts/8 J3 (v2019.06) (mgt01/27020 501), start Wed 9/6 11:08"
	infoUser := parseFlexFeatureUsedDetailLine(userLine, "user")
	if infoUser.User != "user1" || infoUser.Host != "cn07" || infoUser.Version != "v2019.06" || infoUser.Start != "9/6 11:08" {
		t.Errorf("unexpected user parse results: %+v", infoUser)
	}

	// 2. queued
	queuedLine := "user1 cn04 /dev/pts/4 J5 (v2019.06) (mgt01/27020 124) queued for 1 license"
	infoQueued := parseFlexFeatureUsedDetailLine(queuedLine, "queued")
	if infoQueued.User != "user1" || infoQueued.Host != "cn04" || infoQueued.Version != "v2019.06" || infoQueued.Start != "-(queued 1)" || !infoQueued.Queued {
		t.Errorf("unexpected queued parse results: %+v", infoQueued)
	}

	// 3. reservation
	resLine := "1 RESERVATION for HOST_GROUP user1 (lic25/1717)"
	infoRes := parseFlexFeatureUsedDetailLine(resLine, "reservation")
	if infoRes.Host != "user1" || infoRes.Start != "-(RESERVATION 1)" {
		t.Errorf("unexpected reservation parse results: %+v", infoRes)
	}

	// 4. unsupported usedType
	infoNone := parseFlexFeatureUsedDetailLine(userLine, "unsupported")
	if infoNone.User != "" || infoNone.Host != "" {
		t.Errorf("expected empty info, got %+v", infoNone)
	}
}

func TestParseFlexFeatureSummaryInfo(t *testing.T) {
	summary := `
 Feature   Version   #licences  Vendor   Expires
_______   _________ _________   ______   ________
 SSS        1.0        1       snpslmd  18-Feb-2024
 BBB        2.0        5       vendord  Permanent
`
	featuresMap := parseFlexFeatureSummaryInfo(summary)
	if featuresMap["SSS"] != "18-Feb-2024" {
		t.Errorf("expected SSS expire 18-Feb-2024, got %s", featuresMap["SSS"])
	}
	if featuresMap["BBB"] != "Permanent" {
		t.Errorf("expected BBB expire Permanent, got %s", featuresMap["BBB"])
	}
}

func TestParseFlexAllStatusOutput(t *testing.T) {
	// 1. Success case
	allOutput := `lmstat -a output...
Users of SSS:  (Total of 1 license issued;  Total of 1 license in use)
  "SSS" v1.0, vendor: snpslmd, dirty
  floating license
    user1 cn07 /dev/pts/8 J3 (v1.0) (mgt01/27020 501), start Wed 9/6 11:08
    user2 cn04 /dev/pts/4 J5 (v1.0) (mgt01/27020 124) queued for 1 license
    1 RESERVATION for HOST_GROUP user3 (lic25/1717)

Users of BBB:  (Total of 5 licenses issued;  Total of 0 licenses in use)

NOTE: lmstat -i
 Feature   Version   #licences  Vendor   Expires
_______   _________ _________   ______   ________
 SSS        1.0        1       snpslmd  18-Feb-2024
 BBB        2.0        5       vendord  Permanent
`
	list, err := parseFlexAllStatusOutput(allOutput)
	if err != nil {
		t.Fatalf("unexpected error parsing allOutput: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 features parsed, got %d", len(list))
	}

	if list[0].Name != "SSS" || list[0].Expiry != "18-Feb-2024" || list[0].Total != 1 || list[0].Used != 1 {
		t.Errorf("unexpected SSS feature overview: %+v", list[0])
	}
	if len(list[0].UsedBy) != 3 {
		t.Errorf("expected 3 UsedBy users, got %d", len(list[0].UsedBy))
	}
	if list[0].Queued != 1 {
		t.Errorf("expected 1 queued in list[0], got %d", list[0].Queued)
	}

	if list[1].Name != "BBB" || list[1].Expiry != "Permanent" || list[1].Total != 5 || list[1].Used != 0 {
		t.Errorf("unexpected BBB feature overview: %+v", list[1])
	}

	// 2. No split token returns error
	noSplit, err := parseFlexAllStatusOutput("invalid output")
	if err == nil {
		t.Error("expected error for output without split token")
	}
	if noSplit != nil {
		t.Errorf("expected nil for output without split token, got %v", noSplit)
	}
}

func TestParseFlexServerStatus(t *testing.T) {
	output := `
lmstat - Copyright (c) 1989-2018 Flexera. All Rights Reserved.
Flexible License Manager status on Thu 7/9/2026 10:00

License server status: 27020@mgt01
    License file(s) on mgt01: /path/to/lic:
    mgt01: license server UP (v11.16.2)

Vendor daemon status (on mgt01):
  snpslmd: UP (v11.16.2)
`
	up, daemonUp := parseFlexServerStatus(output)
	if !up || !daemonUp {
		t.Errorf("expected server and vendor daemon up, got up=%v, daemonUp=%v", up, daemonUp)
	}

	outputDown := "License server is down..."
	upDown, daemonUpDown := parseFlexServerStatus(outputDown)
	if upDown || daemonUpDown {
		t.Errorf("expected server and daemon down, got up=%v, daemonUp=%v", upDown, daemonUpDown)
	}
}
