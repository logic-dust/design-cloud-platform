package desktop

import "testing"

func TestParseFormatLatestVersionLine(t *testing.T) {
	t.Parallel()

	line := FormatLatestVersionLine("3.1.4", "abcDEF", 42)
	v, h, s, err := ParseLatestVersionLine(line)
	if err != nil || v != "3.1.4" || h != "abcDEF" || s != 42 {
		t.Fatalf("round-trip failed: %v %s %s %d", err, v, h, s)
	}
}
