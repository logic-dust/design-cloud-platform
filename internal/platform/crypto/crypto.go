package crypto

import (
	crand "crypto/rand"
	"fmt"
	"math/rand/v2"
)

// urlSafeChars 是 URL-safe 字母表（64 字符），包含 a-z、A-Z、0-9、- 和 _。
// 64 个字符恰好整除 256（256%64=0），使得 byte%64 的分布完全均匀，无概率偏差。
// 同时适用于文件名、URL、环境变量等所有安全场景。
const urlSafeChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

const (
	letterIdxBits = 6                    // 6 bits to represent a letter index (64 combinations)
	letterIdxMask = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax  = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

// GenSecret generates a cryptographically random string suitable for security
// credentials (e.g. session tokens).
//
// Panics if crypto/rand fails — by design, serving weak credentials is worse
// than not serving at all. Callers should only use this in initialization paths
// where a panic halts the entire process.
func GenSecret(length uint8) string {
	if length == 0 {
		return ""
	}
	b := make([]byte, length)
	if _, err := crand.Read(b); err != nil {
		// 如果生成随机数失败，直接 panic。
		// 安全意图：在涉及安全凭证生成时，如果系统随机源（熵源）失效，必须停止服务以防止生成可预测的弱密钥。
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}

	for i := range b {
		b[i] = urlSafeChars[b[i]&63] // &63 等价于 %64，无分布偏差
	}

	return string(b)
}

// GenRandomId 生成一个随机字符串，适用于临时名称、日志 TraceID 等对安全性要求不高但对性能要求极高的场景。
func GenRandomId(length int) string {
	b := make([]byte, length)
	for i, cache, remain := length-1, rand.Uint64(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = rand.Uint64(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(urlSafeChars) {
			b[i] = urlSafeChars[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}
