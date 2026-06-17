// Package liquibase handles liquibase.properties parsing/rendering and
// two-stage master-changelog tag resolution.
package liquibase

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// masterChangeLog is the XML structure for a master changelog file.
// It only parses <include> elements — tagDatabase lives in included files.
type masterChangeLog struct {
	Includes []include `xml:"include"`
}

type include struct {
	File string `xml:"file,attr"`
}

// changelogFile is the XML structure for an included changelog file.
// We look for <tagDatabase tag="..."/> inside <changeSet> elements.
type changelogFile struct {
	ChangeSets []changeSet `xml:"changeSet"`
}

type changeSet struct {
	TagDatabases []tagDatabase `xml:"tagDatabase"`
}

type tagDatabase struct {
	Tag string `xml:"tag,attr"`
}

// masterChangelogDir is the path relative to clone root where the master
// changelog directory resides.
const masterChangelogDir = "src/main/resources/db/schema/master-changelog"

// FirstTag performs a two-stage lookup:
//  1. Open the single master-changelog XML under masterChangelogDir and parse
//     its <include> list.
//  2. Open the FIRST included changelog file and return the first <tagDatabase tag>
//     value found in any changeSet.
//
// Both "/" and "\" separators in include paths are normalized to the host OS
// path separator before joining with cloneRoot.
func FirstTag(cloneRoot string) (string, error) {
	// Stage 1: find and parse the master-changelog file.
	masterFile, err := findMasterChangelog(cloneRoot)
	if err != nil {
		return "", fmt.Errorf("find master-changelog: %w", err)
	}

	data, err := os.ReadFile(masterFile)
	if err != nil {
		return "", fmt.Errorf("read master-changelog %s: %w", masterFile, err)
	}

	var master masterChangeLog
	if err := xml.Unmarshal(data, &master); err != nil {
		return "", fmt.Errorf("parse master-changelog: %w", err)
	}

	if len(master.Includes) == 0 {
		return "", domain.ErrNoIncludes
	}

	// Stage 2: open the first included file and extract the first tagDatabase.
	firstInclude := master.Includes[0].File
	// Normalize Windows path separators to the host OS separator.
	normalized := strings.ReplaceAll(firstInclude, `\`, "/")
	normalized = filepath.FromSlash(normalized)

	includedPath := filepath.Join(cloneRoot, normalized)
	inclData, err := os.ReadFile(includedPath)
	if err != nil {
		return "", fmt.Errorf("read included changelog %s: %w", includedPath, err)
	}

	var cl changelogFile
	if err := xml.Unmarshal(inclData, &cl); err != nil {
		return "", fmt.Errorf("parse included changelog %s: %w", includedPath, err)
	}

	for _, cs := range cl.ChangeSets {
		for _, td := range cs.TagDatabases {
			if td.Tag != "" {
				return td.Tag, nil
			}
		}
	}

	return "", domain.ErrNoFirstTag
}

// ChangelogResolver implements domain.TagResolver by delegating to FirstTag.
type ChangelogResolver struct{}

// FirstTag satisfies domain.TagResolver for use in orchestrator.Deps.
func (r *ChangelogResolver) FirstTag(cloneRoot string) (string, error) {
	return FirstTag(cloneRoot)
}

// findMasterChangelog returns the path of the single XML file under
// cloneRoot/masterChangelogDir. Returns an error if the directory doesn't exist
// or contains no XML files.
func findMasterChangelog(cloneRoot string) (string, error) {
	dir := filepath.Join(cloneRoot, masterChangelogDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("open master-changelog directory %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".xml") {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no XML file found in %s", dir)
}
