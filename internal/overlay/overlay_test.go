package overlay_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/overlay"
)

func TestOverlay_NestedSubdirsPreserved(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Create nested structure: a/b/c/migration.sql
	nested := filepath.Join(src, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	content := []byte("-- migration")
	if err := os.WriteFile(filepath.Join(nested, "migration.sql"), content, 0o600); err != nil {
		t.Fatalf("write sql: %v", err)
	}

	o := overlay.New()
	copied, err := o.Apply(src, dst)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if copied != 1 {
		t.Errorf("expected 1 file copied, got %d", copied)
	}

	destFile := filepath.Join(dst, "a", "b", "c", "migration.sql")
	got, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("read dest file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestOverlay_StaleDestFileClearedFirst(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Put a stale file in destination that does NOT exist in source.
	staleFile := filepath.Join(dst, "old.sql")
	if err := os.WriteFile(staleFile, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}

	// Source has a different file.
	if err := os.WriteFile(filepath.Join(src, "new.sql"), []byte("new"), 0o600); err != nil {
		t.Fatalf("write new: %v", err)
	}

	o := overlay.New()
	if _, err := o.Apply(src, dst); err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	// Stale file must be gone.
	if _, err := os.Stat(staleFile); !os.IsNotExist(err) {
		t.Error("stale old.sql should have been removed by clear-then-copy")
	}
	// New file must be present.
	if _, err := os.Stat(filepath.Join(dst, "new.sql")); err != nil {
		t.Errorf("new.sql should exist in dest: %v", err)
	}
}

func TestOverlay_EmptySource_ReturnsErrNoPendingSQL(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Source directory exists but has zero .sql files.
	o := overlay.New()
	_, err := o.Apply(src, dst)
	if err == nil {
		t.Fatal("expected error for empty source, got nil")
	}
	if !errors.Is(err, domain.ErrNoPendingSQL) {
		t.Errorf("expected ErrNoPendingSQL, got: %v", err)
	}
}

func TestOverlay_NonSQLFilesNotCopied(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	// Only .sql should be copied; .txt and .xml must not appear in dest.
	if err := os.WriteFile(filepath.Join(src, "migration.sql"), []byte("-- sql"), 0o600); err != nil {
		t.Fatalf("write sql: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "readme.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "changelog.xml"), []byte("<xml/>"), 0o600); err != nil {
		t.Fatalf("write xml: %v", err)
	}

	o := overlay.New()
	copied, err := o.Apply(src, dst)
	if err != nil {
		t.Fatalf("Apply error: %v", err)
	}
	if copied != 1 {
		t.Errorf("expected 1 file copied (only .sql), got %d", copied)
	}

	// Non-SQL files must not appear in dest.
	if _, err := os.Stat(filepath.Join(dst, "readme.txt")); !os.IsNotExist(err) {
		t.Error("readme.txt should NOT be in dest")
	}
	if _, err := os.Stat(filepath.Join(dst, "changelog.xml")); !os.IsNotExist(err) {
		t.Error("changelog.xml should NOT be in dest")
	}
}

func TestOverlay_SourceNotMutated(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	sqlPath := filepath.Join(src, "migration.sql")
	if err := os.WriteFile(sqlPath, []byte("-- sql"), 0o600); err != nil {
		t.Fatalf("write sql: %v", err)
	}

	// Capture source state before Apply.
	beforeInfo, err := os.Stat(sqlPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	beforeContent, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	o := overlay.New()
	if _, err := o.Apply(src, dst); err != nil {
		t.Fatalf("Apply error: %v", err)
	}

	// Source file must be identical after Apply.
	afterInfo, err := os.Stat(sqlPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	afterContent, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}

	if beforeInfo.Size() != afterInfo.Size() {
		t.Errorf("source file size changed: before %d, after %d", beforeInfo.Size(), afterInfo.Size())
	}
	if string(beforeContent) != string(afterContent) {
		t.Errorf("source file content changed after Apply")
	}
}

// Compile-time check: overlay.Overlay must satisfy domain.Overlayer.
var _ domain.Overlayer = (*overlay.Overlay)(nil)
