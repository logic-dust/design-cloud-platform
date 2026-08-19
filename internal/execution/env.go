package execution

import (
	"os"
	"strings"
)

// The following environment variables are known to be dangerous when crossing
// privilege boundaries (e.g. going from root to user, or vice versa).
//
// LD_PRELOAD allows hijacking any library call.
// LD_AUDIT and LD_DEBUG can leak sensitive information or alter behavior.
// LD_LIBRARY_PATH can hijack libraries if an attacker can write to those paths.

var (
	// strictlyBlockedKeys are MUST-REMOVE variables that have no legitimate
	// use in our context and represent immediate high-risk security threats.
	strictlyBlockedKeys = map[string]struct{}{
		"LD_PRELOAD": {},
		"LD_AUDIT":   {},
		"LD_DEBUG":   {},
	}

	// potentiallyDangerousKeys are variables that can be used for attacks
	// but might be necessary for legitimate operations (e.g. LD_LIBRARY_PATH).
	// We block these from untrusted user input, but we might preserve them
	// from the system environment to avoid breaking the runtime.
	potentiallyDangerousKeys = map[string]struct{}{
		"LD_LIBRARY_PATH": {},
	}
)

// IsBlockedEnvKey checks if the given environment variable key is considered
// a security risk. By default, it checks both strictly blocked and potentially
// dangerous keys.
func IsBlockedEnvKey(key string) bool {
	if _, blocked := strictlyBlockedKeys[key]; blocked {
		return true
	}
	if _, blocked := potentiallyDangerousKeys[key]; blocked {
		return true
	}
	return false
}

// IsValidEnvKey returns true when key is safe to emit as an environment
// variable name: non-empty, printable ASCII only, and without '='.
func IsValidEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if c == '=' || c <= 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// CleanCurrentProcessEnv removes dangerous loader variables before privilege changes.
func CleanCurrentProcessEnv() {
	for key := range strictlyBlockedKeys {
		_ = os.Unsetenv(key)
	}
	for key := range potentiallyDangerousKeys {
		_ = os.Unsetenv(key)
	}
}

// BuildCleanEnv returns a new list of environment variables starting with the current process
// environment and safely merging the provided map, ignoring any blocked or sensitive keys.
// Unlike os.Environ(), it guarantees that the returned environment does not contain strictly
// blocked keys, and that user-provided 'extra' variables don't override critical ones.
func BuildCleanEnv(extra map[string]string) []string {
	var env []string

	// 1. First, copy the current environment, but ONLY include safe keys.
	// We explicitly strip strictlyBlockedKeys.
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, blocked := strictlyBlockedKeys[key]; !blocked {
			env = append(env, kv)
		}
	}

	// 2. Then, add extra environment variables, filtering ALL blocked/dangerous keys
	// to prevent untrusted requests from injecting them.
	for key, value := range extra {
		if !IsValidEnvKey(key) || IsBlockedEnvKey(key) {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

// BuildLauncherEnv returns a sanitized environment for invoking dc-launcher.
func BuildLauncherEnv(extra map[string]string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if !IsBlockedEnvKey(key) {
			env = append(env, kv)
		}
	}
	for key, value := range extra {
		if !IsValidEnvKey(key) || IsBlockedEnvKey(key) {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}
