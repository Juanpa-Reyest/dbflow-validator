package liquibase

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// grantToRE extracts one or more role names that appear after the TO keyword
// in a GRANT statement. The regex is intentionally NOT a full SQL parser —
// it performs bounded extraction of GRANT targets only.
//
// Matches patterns like:
//   GRANT ... TO role1
//   GRANT ... TO role1, role2
//   ALTER DEFAULT PRIVILEGES ... GRANT ... TO role1
var grantToRE = regexp.MustCompile(`(?i)\bTO\s+((?:\w+)(?:\s*,\s*\w+)*)`)

// builtinRoles are Postgres pseudo-roles / built-ins that must not be created.
var builtinRoles = map[string]struct{}{
	"PUBLIC":              {},
	"public":              {},
	"pg_read_all_data":    {},
	"pg_write_all_data":   {},
	"pg_read_all_settings": {},
	"pg_read_all_stats":   {},
	"pg_stat_scan_tables": {},
	"pg_signal_backend":   {},
	"pg_monitor":          {},
	"pg_database_owner":   {},
}

// ExtractGrantTargetRoles parses a SQL string and returns the distinct role
// names found after TO in GRANT statements. It excludes PUBLIC and Postgres
// built-in pseudo-roles. The returned slice has no duplicates. Order is
// deterministic (lexicographic).
func ExtractGrantTargetRoles(sql string) []string {
	seen := make(map[string]struct{})
	matches := grantToRE.FindAllStringSubmatch(sql, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		for _, part := range strings.Split(m[1], ",") {
			role := strings.TrimSpace(part)
			if role == "" {
				continue
			}
			// Exclude built-ins (case-insensitive check).
			if _, isBuiltin := builtinRoles[role]; isBuiltin {
				continue
			}
			if _, isBuiltin := builtinRoles[strings.ToUpper(role)]; isBuiltin {
				continue
			}
			seen[role] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]string, 0, len(seen))
	for r := range seen {
		result = append(result, r)
	}
	return result
}

// ExtractGrantTargetRolesFromArchetype walks the archetype directory tree rooted
// at archetypeRoot, collects all GRANT target roles from all .sql files, and
// returns the distinct set (excluding built-ins). Returns nil when none are found.
func ExtractGrantTargetRolesFromArchetype(archetypeRoot string) ([]string, error) {
	seen := make(map[string]struct{})
	err := filepath.WalkDir(archetypeRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".sql") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable files
		}
		for _, r := range ExtractGrantTargetRoles(string(data)) {
			seen[r] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(seen) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(seen))
	for r := range seen {
		result = append(result, r)
	}
	return result, nil
}
