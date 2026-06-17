package liquibase_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
)

// writeFile is a helper that creates a file with given content in dir.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// masterXML produces a databaseChangeLog XML referencing the given include paths.
func masterXML(includePaths ...string) string {
	var includes string
	for _, p := range includePaths {
		includes += `  <include file="` + p + `"/>` + "\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<databaseChangeLog
    xmlns="http://www.liquibase.org/xml/ns/dbchangelog"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    xsi:schemaLocation="http://www.liquibase.org/xml/ns/dbchangelog
        http://www.liquibase.org/xml/ns/dbchangelog/dbchangelog-4.1.xsd">
` + includes + `</databaseChangeLog>
`
}

// changelogXML produces a changelog XML with optional tagDatabase elements.
func changelogXML(tags ...string) string {
	var changeSets string
	for _, tag := range tags {
		changeSets += `  <changeSet id="tag-` + tag + `" author="system">
    <tagDatabase tag="` + tag + `"/>
  </changeSet>
`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<databaseChangeLog
    xmlns="http://www.liquibase.org/xml/ns/dbchangelog"
    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
    xsi:schemaLocation="http://www.liquibase.org/xml/ns/dbchangelog
        http://www.liquibase.org/xml/ns/dbchangelog/dbchangelog-4.1.xsd">
` + changeSets + `</databaseChangeLog>
`
}

func TestFirstTag(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, root string) // sets up files under root
		want    string
		wantErr error
	}{
		{
			name: "tag found via forward-slash include path",
			setup: func(t *testing.T, root string) {
				t.Helper()
				// Create included changelog with tag 210
				includeRelPath := "src/main/resources/db/schema/changelog/DDL/210/xml/N0001.xml"
				writeFile(t, root, includeRelPath, changelogXML("210"))

				// Create master-changelog referencing it with / separator
				masterDir := filepath.Join(root, "src/main/resources/db/schema/master-changelog")
				writeFile(t, root, "src/main/resources/db/schema/master-changelog/master.xml",
					masterXML(includeRelPath))
				_ = masterDir
			},
			want: "210",
		},
		{
			name: "tag found via backslash-separated include path (mandatory Windows-separator case)",
			setup: func(t *testing.T, root string) {
				t.Helper()
				// The actual file is at a normal path
				includeRelPath := "src/main/resources/db/schema/changelog/DDL/210/xml/N0001.xml"
				writeFile(t, root, includeRelPath, changelogXML("210"))

				// master uses backslash as separator (as seen in real archetypes on Windows-built repos)
				backslashPath := `src\main\resources\db\schema\changelog\DDL\210\xml\N0001.xml`
				writeFile(t, root, "src/main/resources/db/schema/master-changelog/master.xml",
					masterXML(backslashPath))
			},
			want: "210",
		},
		{
			name: "multiple tags — only first is returned",
			setup: func(t *testing.T, root string) {
				t.Helper()
				includeRelPath := "src/main/resources/db/schema/changelog/DDL/first/first.xml"
				writeFile(t, root, includeRelPath, changelogXML("100", "200", "300"))
				writeFile(t, root, "src/main/resources/db/schema/master-changelog/master.xml",
					masterXML(includeRelPath))
			},
			want: "100",
		},
		{
			name: "no include elements in master-changelog",
			setup: func(t *testing.T, root string) {
				t.Helper()
				// Master with no includes
				content := `<?xml version="1.0" encoding="UTF-8"?>
<databaseChangeLog xmlns="http://www.liquibase.org/xml/ns/dbchangelog">
</databaseChangeLog>`
				writeFile(t, root, "src/main/resources/db/schema/master-changelog/master.xml", content)
			},
			wantErr: domain.ErrNoIncludes,
		},
		{
			name: "no tagDatabase in included file",
			setup: func(t *testing.T, root string) {
				t.Helper()
				includeRelPath := "src/main/resources/db/schema/changelog/DDL/empty/empty.xml"
				// A changelog with no tagDatabase elements
				content := `<?xml version="1.0" encoding="UTF-8"?>
<databaseChangeLog xmlns="http://www.liquibase.org/xml/ns/dbchangelog">
  <changeSet id="cs1" author="dev">
    <sql>SELECT 1</sql>
  </changeSet>
</databaseChangeLog>`
				writeFile(t, root, includeRelPath, content)
				writeFile(t, root, "src/main/resources/db/schema/master-changelog/master.xml",
					masterXML(includeRelPath))
			},
			wantErr: domain.ErrNoFirstTag,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			tt.setup(t, root)

			got, err := liquibase.FirstTag(root)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("FirstTag() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("FirstTag() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("FirstTag() = %q, want %q", got, tt.want)
			}
		})
	}
}
