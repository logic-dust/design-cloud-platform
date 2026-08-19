package license

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

var (
	reFeatureError = regexp.MustCompile(`Users of (.+):.+Error`)
	reFeatureUsage = regexp.MustCompile(`Users of (.+):.+?(\d+).+?(\d+)`)
	reUserLine     = regexp.MustCompile(`(\w+) (\w+).+\((.+)\) \((.+)\), start.+(\d+/\d+ \d+:\d+)`)
	reQueuedLine   = regexp.MustCompile(`(\w+) (\w+).+\((.+)\) \((.+)\) queued for (\d+) license`)
	reReservation  = regexp.MustCompile(`(\d+) RESERVATION for HOST_GROUP (.+)\s+.+$`)
)

type featureUsedInfo struct {
	User    string `json:"user"`
	Host    string `json:"host"`
	Version string `json:"version"`
	Start   string `json:"start"`
	Queued  bool   `json:"queued"`
}

type featureInfo struct {
	Name   string            `json:"name"`
	Total  int               `json:"total"`
	Used   int               `json:"used"`
	Queued int               `json:"queued"`
	Expiry string            `json:"expiry"`
	UsedBy []featureUsedInfo `json:"usedBy"`
}

func parseFlexFeatureOverviewLine(lineStr string) (featureStatus featureInfo) {
	//Users of calibreXRC:  (Total of 1 license issued;  Total of 1 license in use)
	featureStatus = featureInfo{Total: -1, Used: -1}

	if strings.Contains(lineStr, "Error") {
		//Users of b_pw_core:  (Error: 1 licences, unsupported by licensed server)
		matches := reFeatureError.FindStringSubmatch(lineStr)
		if len(matches) > 1 {
			featureStatus.Name = matches[1]
		}
	} else {
		matches := reFeatureUsage.FindStringSubmatch(lineStr)
		if len(matches) > 3 {
			featureStatus.Name = matches[1]
			if total, err := strconv.Atoi(matches[2]); err == nil {
				featureStatus.Total = total
			}
			if used, err := strconv.Atoi(matches[3]); err == nil {
				featureStatus.Used = used
			}
		}
	}
	return featureStatus
}

func parseFlexFeatureUsedDetailLine(lineStr string, usedType string) featureUsedInfo {
	//user1 cn07 /dev/pts/8 J3_ffffffffa8c00702_31080_1 (v2019.06) (mgt01/27020 501), start Wed 9/6 11:08
	//user1 cn04 /dev/pts/4 J5_ffffffffa8c00402_11351_1 (v2019.06) (mgt01/27020 124) queued for 1 license
	//1 RESERVATION for HOST_GROUP user1 (lic25/1717)
	var info featureUsedInfo
	switch usedType {
	case "user":
		matches := reUserLine.FindStringSubmatch(lineStr)
		if len(matches) > 5 {
			info.User = matches[1]
			info.Host = matches[2]
			info.Version = matches[3]
			//matches[4] is the <licHost>/<port> info
			info.Start = matches[5]
		}
	case "queued":
		matches := reQueuedLine.FindStringSubmatch(lineStr)
		if len(matches) > 5 {
			info.User = matches[1]
			info.Host = matches[2]
			info.Version = matches[3]
			info.Start = fmt.Sprintf("-(queued %s)", matches[5])
			info.Queued = true
		}
	case "reservation":
		matches := reReservation.FindStringSubmatch(lineStr)
		if len(matches) > 2 {
			info.Start = fmt.Sprintf("-(RESERVATION %s)", matches[1])
			info.Host = matches[2]
		}
	}
	return info
}

func parseFlexFeatureSummaryInfo(summaryInfoStr string) map[string]string {
	// Feature   Version   #licences  Vendor   Expires
	//_______   _________ _________   ______   ________
	// SSS        1.0        1       snpslmd  18-Feb-2024
	var featuresExpiryMap = make(map[string]string)
	for line := range strings.SplitSeq(summaryInfoStr, "\n") {
		lineFields := strings.Fields(line)
		if len(lineFields) < 5 {
			continue
		}
		featuresExpiryMap[lineFields[0]] = lineFields[4]
	}
	return featuresExpiryMap
}

// parseFlexAllStatusOutput parses lmstat -a -i -c <lic_file> output
func parseFlexAllStatusOutput(allStatusOutput string) ([]featureInfo, error) {
	//the --- line is the separator of features' used info and features' summary info
	outputParts := strings.Split(allStatusOutput, "NOTE: lmstat -i ")
	if len(outputParts) < 2 {
		// Fallback: some lmstat versions omit the trailing space before the newline.
		outputParts = strings.Split(allStatusOutput, "NOTE: lmstat -i")
		if len(outputParts) < 2 {
			return nil, fmt.Errorf("%w: missing NOTE: lmstat -i token in output", fs.ErrInvalid)
		}
	}

	//map key is featureInfo name, value is expiry date
	featuresExpiryMap := parseFlexFeatureSummaryInfo(outputParts[1])

	parseFeatureUsedInfoFlag := false //parse the user info using the feature
	var featuresList []featureInfo
	i := -1 //index of featuresList

	for line := range strings.SplitSeq(outputParts[0], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// features used info line, e.g.
		// Users of SSS:  (Total of 1 license issued;  Total of 0 licences in use)
		if strings.HasPrefix(line, "Users of") {
			parseFeatureUsedInfoFlag = false
			feature := parseFlexFeatureOverviewLine(line)

			if expiry, ok := featuresExpiryMap[feature.Name]; ok {
				feature.Expiry = expiry
			}

			featuresList = append(featuresList, feature)
			i += 1
			continue
		}

		// find the feature used info detail start flag line: floating license
		if strings.Contains(line, "floating lic") {
			parseFeatureUsedInfoFlag = true
			continue
		}

		// features used info detail line, e.g.
		//user1 cn04 /dev/pts/4 J5_ffffffffa8c00402_3434_1 (v2019.06) (mgt01/27020 234), start Mon 9/18 8:17
		//user1 cn04 /dev/pts/4 J5_ffffffffa8c00402_11351_1 (v2019.06) (mgt01/27020 124) queued for 1 license
		//1 RESERVATION for HOST_GROUP user1 (lic25/1717)
		if parseFeatureUsedInfoFlag {
			switch {
			case strings.Contains(line, "start"):
				featuresList[i].UsedBy = append(featuresList[i].UsedBy, parseFlexFeatureUsedDetailLine(line, "user"))
			case strings.Contains(line, "queued"):
				featuresList[i].UsedBy = append(featuresList[i].UsedBy, parseFlexFeatureUsedDetailLine(line, "queued"))
				featuresList[i].Queued += 1
			case strings.Contains(line, "RESERVATION"):
				featuresList[i].UsedBy = append(featuresList[i].UsedBy, parseFlexFeatureUsedDetailLine(line, "reservation"))
			}
			continue
		}
	}
	return featuresList, nil
}

// parseFlexServerStatus parses lmstat -c <lic_file> output
func parseFlexServerStatus(statusOutputStr string) (isServerUp, isVendorDaemonUp bool) {
	lines := strings.Split(strings.Trim(statusOutputStr, "\n"), "\n")

	for index, line := range lines {
		if strings.Contains(line, "license server UP") {
			isServerUp = true
			continue
		}
		if strings.Contains(line, "Vendor daemon status") {
			allRemainLinesStr := strings.Join(lines[index:], "")
			if strings.Contains(allRemainLinesStr, "UP") {
				isVendorDaemonUp = true
				break
			}
		}
	}

	return isServerUp, isVendorDaemonUp
}
