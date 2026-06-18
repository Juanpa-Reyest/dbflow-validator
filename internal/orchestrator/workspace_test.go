package orchestrator_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

func TestMoveWorkspace_SameFSRename(t *testing.T) {
	// Both src and dst are under t.TempDir (same filesystem) — rename succeeds.
	src := t.TempDir()
	dstParent := t.TempDir()
	dst := filepath.Join(dstParent, "workspace")

	// Write a file into src so we can verify the move preserved contents.
	testFile := filepath.Join(src, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	if err := orchestrator.MoveWorkspace(src, dst); err != nil {
		t.Fatalf("MoveWorkspace: %v", err)
	}

	// dst must exist with the test file inside.
	content, err := os.ReadFile(filepath.Join(dst, "test.txt"))
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("moved file content = %q, want %q", string(content), "hello")
	}

	// src must not exist after the move.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src dir should not exist after move; stat: %v", err)
	}
}

func TestMoveWorkspace_NestedContents(t *testing.T) {
	// Verify that nested directory structure is preserved after move.
	src := t.TempDir()
	subDir := filepath.Join(src, "subdir", "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}
	nestedFile := filepath.Join(subDir, "nested.txt")
	if err := os.WriteFile(nestedFile, []byte("nested"), 0o644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	dstParent := t.TempDir()
	dst := filepath.Join(dstParent, "workspace")

	if err := orchestrator.MoveWorkspace(src, dst); err != nil {
		t.Fatalf("MoveWorkspace nested: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dst, "subdir", "nested", "nested.txt"))
	if err != nil {
		t.Fatalf("read nested moved file: %v", err)
	}
	if string(content) != "nested" {
		t.Errorf("nested content = %q, want %q", string(content), "nested")
	}
}
