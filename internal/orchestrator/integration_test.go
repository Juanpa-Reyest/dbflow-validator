package orchestrator_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/embedrepo"
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
	internalgit "github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/overlay"
	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
)

// TestEndToEnd_HappyPath runs the REAL flow against the reference archetype
// db-artifacts-scgolfcore. It requires Docker to be running and mvn cached in ~/.m2.
//
// This test VALIDATES the reverse-engineered Maven constants:
//   - GoalSync   = "dbflow:sync"
//   - GoalRollback = "dbflow:rollback"
//   - params format: space-separated "--KEY=VALUE" pairs
func TestEndToEnd_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end integration test in -short mode (requires Docker + Maven)")
	}

	// The reference archetype is relative to the workspace directory.
	// Path from internal/orchestrator: ../../../db-artifacts-scgolfcore
	archetypeSrc := filepath.Join("..", "..", "..", "db-artifacts-scgolfcore")
	archetypeSrc, err := filepath.Abs(archetypeSrc)
	if err != nil {
		t.Fatalf("resolve archetype path: %v", err)
	}

	if _, err := os.Stat(archetypeSrc); err != nil {
		t.Skipf("reference archetype not found at %s: %v", archetypeSrc, err)
	}

	// Copy the archetype to a temp dir so the flow can mutate liquibase.properties
	// without touching the read-only source. The clone's SQLInput is intentionally
	// left empty — the overlay step will populate it from fixtureLocalSQLInput below.
	tmpArchetype := t.TempDir()
	t.Logf("copying archetype to %s", tmpArchetype)
	if err := copyDir(archetypeSrc, tmpArchetype); err != nil {
		t.Fatalf("copy archetype: %v", err)
	}

	// Create a SEPARATE fixture local SQLInput directory that simulates the developer's
	// local working copy. The overlay step will copy these files into the clone's
	// src/main/resources/SQLInput/ before sync.
	//
	// The plugin validates file names against: (N/U)(4 digits)_TYPE_DESCRIPTION.sql
	// where TYPE ∈ {TA, SP, FN, PA, IX, SE, TS, PK, FK, TY, DML, GRT, USR, TBS, INS, DEL, UPD}.
	// A TA (table) file causes the plugin to fall back to the full Liquibase XML changelog
	// update — correct behavior for initial validation of a new ephemeral DB.
	// The lb_scgolfcore schema is created by the orchestrator's schema-setup step.
	fixtureLocalSQLInput := t.TempDir()
	testSQL := `CREATE TABLE IF NOT EXISTS lb_scgolfcore.validator_run (
  id SERIAL PRIMARY KEY,
  run_tag TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT now()
);`
	testSQLRB := `DROP TABLE IF EXISTS lb_scgolfcore.validator_run;`
	if err := os.WriteFile(filepath.Join(fixtureLocalSQLInput, "N0001_TA_VALIDATOR_RUN.sql"), []byte(testSQL), 0o644); err != nil {
		t.Fatalf("write SQLInput test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixtureLocalSQLInput, "N0001_TA_VALIDATOR_RUN_RB.sql"), []byte(testSQLRB), 0o644); err != nil {
		t.Fatalf("write SQLInput rollback file: %v", err)
	}
	t.Logf("fixture local SQLInput prepared at %s with N0001_TA_VALIDATOR_RUN.sql", fixtureLocalSQLInput)

	// Create a per-run Docker network so Postgres and Maven containers share alias resolution.
	ctx := context.Background()
	_, networkName, networkCleanup, netErr := container.NewNetwork(ctx)
	if netErr != nil {
		t.Fatalf("create docker network: %v", netErr)
	}
	t.Cleanup(func() { _ = networkCleanup() })
	t.Logf("docker network: %s", networkName)

	// Wire real adapters.
	pgContainerProvider := container.NewPostgresProvider(networkName)
	dbEng, err := engine.ProviderFor(engine.EnginePostgres)
	if err != nil {
		t.Fatalf("engine provider: %v", err)
	}

	realDBProvider := &realPostgresDBProvider{
		eng:      dbEng,
		provider: pgContainerProvider,
	}

	// Use a fake cloner that just returns the tmpArchetype path
	// (avoids needing a real git remote for the e2e test).
	fakeCloner := &localCloner{root: tmpArchetype}

	// Extract the embedded vendored Maven repo to a temp cache dir.
	// This validates the full embedded-cache path (no host mvn/JVM required).
	embeddedCacheRoot := t.TempDir()
	mavenRepoCachePath, extractErr := embedrepo.EnsureExtracted(embeddedCacheRoot, "test")
	if extractErr != nil {
		t.Fatalf("extract embedded Maven repo: %v", extractErr)
	}
	t.Logf("using embedded Maven repo cache: %s", mavenRepoCachePath)

	// Maven runs inside a container on the shared Docker network.
	// Host Maven/JVM are NOT used — this validates the zero-friction distribution path.
	containerRunner := maven.NewContainerRunner(
		maven.DefaultImage,
		networkName,
		mavenRepoCachePath,
		os.Getuid(),
		os.Getgid(),
	)

	deps := orchestrator.Deps{
		Preflight:          preflight.New(nil),
		Cloner:             fakeCloner,
		DBProvider:         realDBProvider,
		Patcher:            liquibase.NewPatcher(),
		Engine:             engine.NewDetector(),
		Tags:               &liquibase.ChangelogResolver{},
		Maven:              containerRunner,
		NetworkCleanup:     networkCleanup,
		MavenRepoCachePath: mavenRepoCachePath,
		// Wire the real Overlayer so fixture SQLInput is copied into the clone's
		// src/main/resources/SQLInput/ before sync.
		Overlayer: overlay.New(),
	}

	cfg := config.Config{
		RepoURL:      "local://db-artifacts-scgolfcore",
		BaseBranch:   "integration",
		SQLInputPath: fixtureLocalSQLInput,
		Token:        domain.NewSecret(""),
	}

	t.Log("Starting end-to-end validation run...")
	rpt := orchestrator.Run(ctx, deps, cfg)

	t.Logf("Overall status: %s", rpt.Status)
	for _, step := range rpt.Steps {
		t.Logf("  [%s] %s (%d ms)", step.Status, step.Name, step.DurationMs)
		if step.Error != "" {
			t.Logf("    Error: %s", step.Error)
		}
		if step.Trace != "" {
			// Print only the last 30 lines of trace to keep output manageable.
			t.Logf("    Trace (tail):\n%s", tailLines(step.Trace, 30))
		}
	}

	if rpt.Status != domain.StatusPassed {
		t.Errorf("expected PASSED, got %v", rpt.Status)
	}

	// Assert that the overlay step is present, PASSED, and positioned correctly.
	engineGuardIdx := -1
	overlayIdx := -1
	containerStartIdx := -1
	for i, s := range rpt.Steps {
		switch s.Name {
		case "engine-guard":
			engineGuardIdx = i
		case "overlay":
			overlayIdx = i
		case "container-start":
			containerStartIdx = i
		}
	}
	if overlayIdx == -1 {
		t.Error("expected 'overlay' step in report, not found")
	} else {
		if rpt.Steps[overlayIdx].Status != domain.StepStatusPassed {
			t.Errorf("overlay step: expected PASSED, got %v (error: %s)",
				rpt.Steps[overlayIdx].Status, rpt.Steps[overlayIdx].Error)
		}
		if engineGuardIdx != -1 && overlayIdx <= engineGuardIdx {
			t.Errorf("overlay (%d) must come after engine-guard (%d)", overlayIdx, engineGuardIdx)
		}
		if containerStartIdx != -1 && overlayIdx >= containerStartIdx {
			t.Errorf("overlay (%d) must come before container-start (%d)", overlayIdx, containerStartIdx)
		}
	}

	// Assert that the source fixture is never mutated (fixture dir must still have its files).
	// Note: the clone's SQLInput is cleaned up by orchestrator.Run's deferred cleanup,
	// so we verify the overlay outcome via the PASSED step status above rather than
	// checking the post-run file system state of the clone directory.
	if _, err := os.Stat(filepath.Join(fixtureLocalSQLInput, "N0001_TA_VALIDATOR_RUN.sql")); err != nil {
		t.Errorf("fixture source file should still exist after overlay (source must never be mutated): %v", err)
	}
}

// ---- helpers ----

// localCloner is a fake that returns a pre-existing directory (no git clone).
type localCloner struct{ root string }

func (c *localCloner) Clone(_ context.Context, _ domain.CloneOptions) (string, error) {
	return c.root, nil
}

// realPostgresDBProvider wires the real PostgresProvider and Ping.
type realPostgresDBProvider struct {
	eng      domain.DatabaseProvider
	provider *container.PostgresProvider
}

func (p *realPostgresDBProvider) Image() string { return p.eng.Image() }
func (p *realPostgresDBProvider) ContainerProvider() domain.ContainerProvider {
	return p.provider
}
func (p *realPostgresDBProvider) DSN(coords domain.ContainerCoords) string {
	return p.eng.DSN(coords)
}
func (p *realPostgresDBProvider) Ping(ctx context.Context, dsn string) error {
	return container.Ping(ctx, dsn)
}

// copyDir copies src directory tree to dst using os/exec cp -r for simplicity.
func copyDir(src, dst string) error {
	return exec.Command("cp", "-r", src+"/.", dst).Run()
}

// fakeE2ECloner performs a real git clone into a local temp dir.
// Used when testing with a real local bare repo.
type fakeE2ECloner struct{}

func (c *fakeE2ECloner) Clone(ctx context.Context, opts domain.CloneOptions) (string, error) {
	return internalgit.NewCloner(nil, os.MkdirAll).Clone(ctx, opts)
}

// tailLines returns at most n trailing lines from s.
func tailLines(s string, n int) string {
	lines := splitLines(s)
	if len(lines) <= n {
		return s
	}
	return joinLines(lines[len(lines)-n:])
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func joinLines(lines []string) string {
	result := make([]byte, 0, len(lines)*80)
	for i, l := range lines {
		result = append(result, l...)
		if i < len(lines)-1 {
			result = append(result, '\n')
		}
	}
	return string(result)
}

// fakeGitCloner is kept to avoid unused import of internalgit in cases where
// localCloner is used instead.
var _ io.Reader = (*fakeGitCloner)(nil)

type fakeGitCloner struct{}

func (f *fakeGitCloner) Read(p []byte) (int, error) { return 0, io.EOF }
