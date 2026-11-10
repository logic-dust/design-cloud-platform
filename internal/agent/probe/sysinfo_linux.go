package probe

import (
	"bufio"
	"bytes"
	"cmp"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func detectSysInfo() SysInfo {
	name, version := parseOSRelease()
	kernel := readKernelRelease()
	return SysInfo{
		OSName:        name,
		OSVersion:     version,
		KernelVersion: kernel,
	}
}

func parseOSRelease() (string, string) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	vals := map[string]string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[key] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return cmp.Or(vals["PRETTY_NAME"], vals["NAME"]), cmp.Or(vals["VERSION_ID"], vals["VERSION"])
}

func readKernelRelease() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		return ""
	}
	return unixByteToString(u.Release[:])
}

func unixByteToString(arr []byte) string {
	n := 0
	for n < len(arr) && arr[n] != 0 {
		n++
	}
	return string(arr[:n])
}
