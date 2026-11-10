package crypto

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVerifyShadowHash_SHA512(t *testing.T) {
	tests := []struct {
		name     string
		password string
		hash     string
		valid    bool
	}{
		{
			name:     "openssl sha512 hello world",
			password: "Hello world!",
			hash:     "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1",
			valid:    true,
		},
		{
			name:     "openssl sha512 with long salt truncated to 16",
			password: "testpassword123",
			hash:     "$6$toolongsaltstrin$qLOEt27gflWCai35Sr1g0w6lln9weUJqymjTchrIXpTH135J6gaI5Wg9llGcTHS9gYatdnocJrIKSAcKjqltg.",
			valid:    true,
		},
		{
			name:     "openssl sha512 complex password and symbols",
			password: "MyComplexP@ssw0rd#$%",
			hash:     "$6$abc123XYZ$33icEpz7HFTKPLpmLI5Y2nWY47ioa3rGsDb2JComPWlOuPveLYl8lXHZgVaqWjd5Fp2C.nfCJ7sMxpqe7E0yT1",
			valid:    true,
		},
		{
			name:     "mkpasswd sha512 LinuxAdmin",
			password: "LinuxAdmin2026!",
			hash:     "$6$mkSalt512$4W7knfmjspMQwavQe31/ho5X/ESbcAhtSGKDOxiWL1o.3fR1NghShgvOHQ6kuexs.Hm8dQWAB.aFWXFGJwcYs.",
			valid:    true,
		},
		{
			name:     "mkpasswd sha512 custom rounds=8000",
			password: "ComplexRoundsPassword",
			hash:     "$6$rounds=8000$salt1234$4hxr5vcxt8u0IeKxksC2gqh3LYt4d.FRJye00C302KewTOXxyf.swmBBTOSCmwErgkM0UfIKAblyZ4dIumLWp0",
			valid:    true,
		},
		{
			name:     "glibc native crypt(3) rounds=6543",
			password: "LinuxGlibc2026",
			hash:     "$6$rounds=6543$glibcSalt$BTBXrbNntt2Ycz5cTnQ1d3e64c6l.6gqU26GWV/Joza6IJ4QHkewYmo9sjg71t/0zcaYoO/jS0Kt.TZlOsq0v/",
			valid:    true,
		},
		{
			name:     "sha512 wrong password",
			password: "Wrong password",
			hash:     "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1",
			valid:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VerifyShadowHash(tc.password, tc.hash)
			if got != tc.valid {
				t.Errorf("VerifyShadowHash(%q, %q) = %v; want %v", tc.password, tc.hash, got, tc.valid)
			}
		})
	}
}

// TestVerifyShadowHash_DynamicOpenSSLCrossCheck tests against dynamically invoked openssl command if available.
func TestVerifyShadowHash_DynamicOpenSSLCrossCheck(t *testing.T) {
	opensslPath, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl binary not found on host, skipping dynamic cross-check")
	}

	testCases := []struct {
		salt     string
		password string
	}{
		{"dynSalt512", "P@ssw0rdDynamic99!"},
		{"anotherSalt", "Simple123"},
		{"longSaltMoreThan16Chars", "TestPass1234"},
	}

	for _, tc := range testCases {
		cmd := exec.Command(opensslPath, "passwd", "-6", "-salt", tc.salt, tc.password)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("openssl command failed: %v", err)
		}
		hash := strings.TrimSpace(string(out))

		if !VerifyShadowHash(tc.password, hash) {
			t.Errorf("VerifyShadowHash failed for dynamically generated openssl hash: %s (password: %s)", hash, tc.password)
		}
		if VerifyShadowHash("wrong_"+tc.password, hash) {
			t.Errorf("VerifyShadowHash unexpectedly succeeded for wrong password with hash: %s", hash)
		}
	}
}

func TestVerifyShadowHash_InvalidInputs(t *testing.T) {
	invalidHashes := []string{
		"",
		"$",
		"$5$unsupported",
		"$1$unsupported",
		"$2a$invalid",
		"$unknown$salt$hash",
		"plainpassword",
	}

	for _, h := range invalidHashes {
		if VerifyShadowHash("test", h) {
			t.Errorf("expected invalid hash %q to return false", h)
		}
	}
}
