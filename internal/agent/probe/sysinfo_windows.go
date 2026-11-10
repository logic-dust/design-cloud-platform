package probe

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

func detectSysInfo() SysInfo {
	name, version := readWindowsProduct()
	return SysInfo{
		OSName:    name,
		OSVersion: version,
	}
}

func readWindowsProduct() (name, version string) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer func() { _ = k.Close() }()

	productName, _, err := k.GetStringValue("ProductName")
	if err == nil {
		name = productName
	}
	displayVersion, _, err := k.GetStringValue("DisplayVersion")
	if err == nil && displayVersion != "" {
		version = displayVersion
	} else if build, _, err := k.GetStringValue("CurrentBuildNumber"); err == nil {
		ubr, _, _ := k.GetIntegerValue("UBR")
		if ubr > 0 {
			version = fmt.Sprintf("%s.%d", build, ubr)
		} else {
			version = build
		}
	}
	return name, version
}
