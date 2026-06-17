package liquibase

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// schemaRE matches:
//   CREATE SCHEMA [IF NOT EXISTS] <schema_name>
// Case-insensitive. The schema name may be a plain identifier or a double-quoted
// identifier. Group 1 captures the raw name (with quotes stripped separately).
var schemaRE = regexp.MustCompile(
	`(?i)CREATE\s+SCHEMA\s+(?:IF\s+NOT\s+EXISTS\s+)?("?[\w]+"?)`,
)

// ExtractSchemaName reads the SQL file at sqlPath and returns the first schema
// name found in a CREATE SCHEMA statement. Quoted identifiers have their quotes
// stripped. Returns an error when no CREATE SCHEMA statement is found.
func ExtractSchemaName(sqlPath string) (string, error) {
	data, err := os.ReadFile(sqlPath)
	if err != nil {
		return "", fmt.Errorf("ExtractSchemaName: read %q: %w", sqlPath, err)
	}
	return extractSchemaFromSQL(string(data))
}

// extractSchemaFromSQL extracts the first schema name from a SQL string.
func extractSchemaFromSQL(sql string) (string, error) {
	m := schemaRE.FindStringSubmatch(sql)
	if m == nil {
		return "", fmt.Errorf("no CREATE SCHEMA statement found in SQL")
	}
	name := m[1]
	// Strip double quotes if present.
	name = strings.Trim(name, `"`)
	return name, nil
}

// LbUsername returns the ephemeral Liquibase connection user derived from the
// schema name following the archetype convention: lb_<schema>.
func LbUsername(schemaName string) string {
	return "lb_" + schemaName
}

// ExtractSchemaFromArchetype walks the archetype directory tree rooted at
// archetypeRoot, finds the first SQL file that contains a CREATE SCHEMA statement,
// and returns the schema name.
//
// This is used to derive the lb_<schema> connection user without hardcoding
// schema names.
func ExtractSchemaFromArchetype(archetypeRoot string) (string, error) {
	var schema string
	err := filepath.WalkDir(archetypeRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".sql") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			// Skip unreadable files.
			return nil
		}
		name, parseErr := extractSchemaFromSQL(string(data))
		if parseErr != nil {
			// No CREATE SCHEMA in this file — keep walking.
			return nil
		}
		schema = name
		return filepath.SkipAll // stop after first match
	})
	if err != nil {
		return "", fmt.Errorf("ExtractSchemaFromArchetype: walk %q: %w", archetypeRoot, err)
	}
	if schema == "" {
		return "", fmt.Errorf("ExtractSchemaFromArchetype: no CREATE SCHEMA statement found under %q", archetypeRoot)
	}
	return schema, nil
}
