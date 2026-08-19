package format

import (
	"math"
)

const (
	kbInMB = int64(1 << 10) // 1,024 KB = 1 MB
	kbInGB = int64(1 << 20) // 1,048,576 KB = 1 GB
	kbInTB = int64(1 << 30) // 1,073,741,824 KB = 1 TB
	kbInPB = int64(1 << 40) // 1,099,511,627,776 KB = 1 PB
)

// AutoScaleKB converts a KB value into a human-readable float64 value and unit (MB, GB, TB, PB).
// It rounds to 2 decimal places. Values smaller than 1MB (1024 KB) are represented as MB (e.g. 0.5 MB).
func AutoScaleKB(kb int64) (float64, string) {
	var value float64
	var unit string

	switch {
	case kb >= kbInPB:
		value = float64(kb) / float64(kbInPB)
		unit = "PB"
	case kb >= kbInTB:
		value = float64(kb) / float64(kbInTB)
		unit = "TB"
	case kb >= kbInGB:
		value = float64(kb) / float64(kbInGB)
		unit = "GB"
	default:
		value = float64(kb) / float64(kbInMB)
		unit = "MB"
	}

	return RoundTo(value, 2), unit
}

// ScaleKBTo scales a KB value to the given unit (MB, GB, TB, PB).
func ScaleKBTo(kb int64, unit string) float64 {
	var value float64
	switch unit {
	case "PB":
		value = float64(kb) / float64(kbInPB)
	case "TB":
		value = float64(kb) / float64(kbInTB)
	case "GB":
		value = float64(kb) / float64(kbInGB)
	case "MB":
		value = float64(kb) / float64(kbInMB)
	default:
		value = float64(kb)
	}
	return RoundTo(value, 2)
}

func RoundTo(val float64, precision int) float64 {
	p := math.Pow(10, float64(precision))
	return math.Round(val*p) / p
}
