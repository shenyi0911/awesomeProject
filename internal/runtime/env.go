package runtime

import (
	"os"
	"strconv"
	"strings"
)

// allowedEnvKeys is the allowlist of environment variables exposed to algorithm processes.
// Platform secrets (database DSN, object storage keys, API tokens) must never leak to uploaded code.
var allowedEnvKeys = []string{
	"PATH", "Path",
	"PATHEXT",
	"SystemRoot", "SYSTEMROOT", "WINDIR",
	"ComSpec",
	"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
	"APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
	"ProgramFiles", "ProgramFiles(x86)",
	"NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE",
	"TEMP", "TMP", "TMPDIR",
	"LANG", "LC_ALL", "LC_CTYPE",
	"PYTHONHOME", "PYTHONPATH", "VIRTUAL_ENV",
	"PYTHONIOENCODING",
}

// sanitizedEnv returns only allowlisted environment variables.
func sanitizedEnv() []string {
	seen := make(map[string]bool, len(allowedEnvKeys))
	out := make([]string, 0, len(allowedEnvKeys))
	for _, key := range allowedEnvKeys {
		upper := strings.ToUpper(key)
		if seen[upper] {
			continue
		}
		seen[upper] = true
		if v, ok := os.LookupEnv(key); ok {
			out = append(out, key+"="+v)
		}
	}
	return out
}

// algorithmEnv returns the sanitized environment plus the algorithm listen port.
func algorithmEnv(appPort int) []string {
	env := sanitizedEnv()
	if appPort > 0 {
		env = append(env, "APP_PORT="+strconv.Itoa(appPort))
	}
	return env
}
