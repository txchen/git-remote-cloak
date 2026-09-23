// Package gitexec defines the environment boundary for Git subprocesses that
// explicitly select a repository rather than inheriting the caller's repository.
package gitexec

import "strings"

// Environment removes repository-location overrides while retaining transport
// configuration and authentication (including Git's command-line config).
// Callers may append intentional overrides after this boundary.
func Environment(environment []string) []string {
	clean := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "GIT_DIR", "GIT_COMMON_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_NOSYSTEM":
			continue
		}
		clean = append(clean, entry)
	}
	return append(clean, "GIT_CONFIG_NOSYSTEM=1")
}
