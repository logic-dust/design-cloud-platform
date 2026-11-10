package crypto

import (
	"strings"
	"testing"
)

func TestGenSecret_Lengths(t *testing.T) {
	t.Parallel()

	for _, length := range []uint8{1, 8, 16, 32, 64, 128} {
		result := GenSecret(length)
		if len(result) != int(length) {
			t.Errorf("GenSecret(%d) length = %d, want %d", length, len(result), length)
		}
	}
}

func TestGenSecret_Empty(t *testing.T) {
	t.Parallel()

	result := GenSecret(0)
	if result != "" {
		t.Errorf("GenSecret(0) = %q, want empty string", result)
	}
}

func TestGenSecret_Charset(t *testing.T) {
	t.Parallel()

	// Generate a long enough string to exercise the character set.
	result := GenSecret(255)
	for _, ch := range result {
		if !strings.ContainsRune(urlSafeChars, ch) {
			t.Errorf("GenSecret contains invalid character: %q (0x%x)", ch, ch)
		}
	}
}

func TestGenSecret_Distribution(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("distribution test skipped in short mode")
	}

	// Generate multiple batches to accumulate enough data for a distribution check.
	var allBytes []byte
	for range 300 {
		allBytes = append(allBytes, []byte(GenSecret(200))...)
	}

	n := len(allBytes)
	freq := make(map[byte]int)
	for _, b := range allBytes {
		freq[b]++
	}

	expectedPerBucket := n / 64
	margin := expectedPerBucket / 4 // 25% tolerance
	for ch, count := range freq {
		if count < expectedPerBucket-margin || count > expectedPerBucket+margin {
			t.Errorf("character %q (0x%x) count = %d, want ~%d ± %d", ch, ch, count, expectedPerBucket, margin)
		}
	}
}

func TestGenRandomId_Lengths(t *testing.T) {
	t.Parallel()

	for _, length := range []int{1, 4, 16, 64} {
		result := GenRandomId(length)
		if len(result) != length {
			t.Errorf("GenRandomId(%d) length = %d, want %d", length, len(result), length)
		}
	}
}

func TestGenRandomId_Charset(t *testing.T) {
	t.Parallel()

	result := GenRandomId(2048)
	for _, ch := range result {
		if !strings.ContainsRune(urlSafeChars, ch) {
			t.Errorf("GenRandomId contains invalid character: %q (0x%x)", ch, ch)
		}
	}
}

func TestGenRandomId_NotConstant(t *testing.T) {
	t.Parallel()

	a := GenRandomId(64)
	b := GenRandomId(64)
	if a == b {
		t.Error("two GenRandomId calls returned same string (extremely unlikely)")
	}
}

func TestGenSecret_PanicsOnZeroLength(t *testing.T) {
	// GenSecret(0) returns "" — no panic. Only crypto/rand failure panics.
	t.Parallel()
	result := GenSecret(0)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}
