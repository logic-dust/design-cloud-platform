package crypto

import (
	"crypto/sha512"
	"crypto/subtle"
	"strconv"
	"strings"
)

const (
	b64Chars = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	defaultRounds = 5000
	minRounds     = 1000
	maxRounds     = 999999999
)

// VerifyShadowHash verifies a plaintext password against a standard Unix /etc/shadow
// SHA-512 ($6$) password hash string.
//
// Comparison is performed in constant time to prevent timing attacks.
func VerifyShadowHash(password, hashedPassword string) bool {
	if !strings.HasPrefix(hashedPassword, "$6$") {
		return false
	}

	computed, err := cryptSha512(password, hashedPassword)
	if err != nil {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(computed), []byte(hashedPassword)) == 1
}

// b64from24bit encodes 24 bits into n characters of custom crypt Base64.
func b64from24bit(b2, b1, b0 byte, n int, sb *strings.Builder) {
	w := (uint32(b2) << 16) | (uint32(b1) << 8) | uint32(b0)
	for n > 0 {
		sb.WriteByte(b64Chars[w&0x3f])
		w >>= 6
		n--
	}
}

// parseSha512Setting extracts rounds, salt, and whether custom rounds was explicitly specified.
func parseSha512Setting(setting string) (rounds int, customRounds bool, salt string, err error) {
	// Format: $6$[rounds=N$]salt...
	parts := strings.Split(setting, "$")
	// Expected parts: ["", "6", "salt", ...] or ["", "6", "rounds=N", "salt", ...]
	if len(parts) < 3 || parts[1] != "6" {
		return 0, false, "", strconv.ErrSyntax
	}

	rounds = defaultRounds
	saltIdx := 2

	if strings.HasPrefix(parts[2], "rounds=") {
		customRounds = true
		rStr := strings.TrimPrefix(parts[2], "rounds=")
		r, err := strconv.Atoi(rStr)
		if err != nil {
			return 0, false, "", err
		}
		rounds = min(max(r, minRounds), maxRounds)
		saltIdx = 3
	}

	if len(parts) <= saltIdx {
		return 0, false, "", strconv.ErrSyntax
	}

	salt = parts[saltIdx]
	if len(salt) > 16 {
		salt = salt[:16]
	}

	return rounds, customRounds, salt, nil
}

// cryptSha512 implements Ulrich Drepper's SHA-512 crypt ($6$).
func cryptSha512(password, setting string) (string, error) {
	rounds, customRounds, salt, err := parseSha512Setting(setting)
	if err != nil {
		return "", err
	}

	key := []byte(password)
	saltBytes := []byte(salt)

	// Step 1..3: Intermediate digest B
	hB := sha512.New()
	hB.Write(key)
	hB.Write(saltBytes)
	hB.Write(key)
	altResult := hB.Sum(nil)

	// Step 4..8: Initial digest A
	hA := sha512.New()
	hA.Write(key)
	hA.Write(saltBytes)

	digestLen := len(altResult)
	for i := len(key); i > 0; i -= digestLen {
		take := min(i, digestLen)
		hA.Write(altResult[:take])
	}

	for i := len(key); i > 0; i >>= 1 {
		if (i & 1) != 0 {
			hA.Write(altResult)
		} else {
			hA.Write(key)
		}
	}
	pBytes := hA.Sum(nil)

	// Step 9..11: Digest DP (password sequence)
	hDP := sha512.New()
	for range key {
		hDP.Write(key)
	}
	dpResult := hDP.Sum(nil)

	pSeq := make([]byte, 0, len(key))
	for len(pSeq) < len(key) {
		take := min(len(key)-len(pSeq), len(dpResult))
		pSeq = append(pSeq, dpResult[:take]...)
	}

	// Step 12..15: Digest DS (salt sequence)
	hDS := sha512.New()
	count := 16 + int(pBytes[0])
	for range count {
		hDS.Write(saltBytes)
	}
	dsResult := hDS.Sum(nil)

	sSeq := make([]byte, 0, len(saltBytes))
	for len(sSeq) < len(saltBytes) {
		take := min(len(saltBytes)-len(sSeq), len(dsResult))
		sSeq = append(sSeq, dsResult[:take]...)
	}

	// Step 16..20: 5000 rounds loop
	curDigest := pBytes
	for i := range rounds {
		hLoop := sha512.New()
		if (i & 1) != 0 {
			hLoop.Write(pSeq)
		} else {
			hLoop.Write(curDigest)
		}

		if (i % 3) != 0 {
			hLoop.Write(sSeq)
		}
		if (i % 7) != 0 {
			hLoop.Write(pSeq)
		}

		if (i & 1) != 0 {
			hLoop.Write(curDigest)
		} else {
			hLoop.Write(pSeq)
		}
		curDigest = hLoop.Sum(nil)
	}

	// Step 21: Encode Base64
	var sb strings.Builder
	sb.WriteString("$6$")
	if customRounds {
		sb.WriteString("rounds=")
		sb.WriteString(strconv.Itoa(rounds))
		sb.WriteByte('$')
	}
	sb.WriteString(salt)
	sb.WriteByte('$')

	c := curDigest
	b64from24bit(c[0], c[21], c[42], 4, &sb)
	b64from24bit(c[22], c[43], c[1], 4, &sb)
	b64from24bit(c[44], c[2], c[23], 4, &sb)
	b64from24bit(c[3], c[24], c[45], 4, &sb)
	b64from24bit(c[25], c[46], c[4], 4, &sb)
	b64from24bit(c[47], c[5], c[26], 4, &sb)
	b64from24bit(c[6], c[27], c[48], 4, &sb)
	b64from24bit(c[28], c[49], c[7], 4, &sb)
	b64from24bit(c[50], c[8], c[29], 4, &sb)
	b64from24bit(c[9], c[30], c[51], 4, &sb)
	b64from24bit(c[31], c[52], c[10], 4, &sb)
	b64from24bit(c[53], c[11], c[32], 4, &sb)
	b64from24bit(c[12], c[33], c[54], 4, &sb)
	b64from24bit(c[34], c[55], c[13], 4, &sb)
	b64from24bit(c[56], c[14], c[35], 4, &sb)
	b64from24bit(c[15], c[36], c[57], 4, &sb)
	b64from24bit(c[37], c[58], c[16], 4, &sb)
	b64from24bit(c[59], c[17], c[38], 4, &sb)
	b64from24bit(c[18], c[39], c[60], 4, &sb)
	b64from24bit(c[40], c[61], c[19], 4, &sb)
	b64from24bit(c[62], c[20], c[41], 4, &sb)
	b64from24bit(0, 0, c[63], 2, &sb)

	return sb.String(), nil
}
