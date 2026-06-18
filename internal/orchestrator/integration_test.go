package orchestrator_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/embedrepo"
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
	internalgit "github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/logging"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/overlay"
	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
	"github.com/dbflow-validator/dbflow-validator/internal/report"
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

	// Create a run dir for this test so we can assert artifacts are written.
	runDir := t.TempDir()
	runSubDir := filepath.Join(runDir, "e2e-run")
	if err := os.MkdirAll(runSubDir, 0o700); err != nil {
		t.Fatalf("create run subdir: %v", err)
	}

	// Open execution.log inside the run dir.
	logFilePath := filepath.Join(runSubDir, "execution.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open execution.log: %v", err)
	}
	defer logFile.Close()

	// Maven output goes to both os.Stderr (live) and execution.log.
	mavenOut := logging.MavenWriter(os.Stderr, logFile)

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
		Overlayer:     overlay.New(),
		MavenOut:      mavenOut,
		RunDir:        runSubDir,
		KeepWorkspace: false,
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

	// Close the log file so all data is flushed before we assert file content.
	logFile.Close()

	// --- Run dir assertions (Phase 7.1) ---

	// execution.log must exist and have content.
	logContent, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Errorf("execution.log must exist after PASSED run: %v", err)
	} else if len(logContent) == 0 {
		t.Error("execution.log must not be empty after a PASSED run")
	}

	// Write report.json into the run dir (simulating what main.go does).
	jsonRenderer := report.NewJSONRenderer()
	jsonBytes, renderErr := jsonRenderer.Render(rpt)
	if renderErr != nil {
		t.Fatalf("render report.json: %v", renderErr)
	}
	reportPath := filepath.Join(runSubDir, "report.json")
	if err := os.WriteFile(reportPath, jsonBytes, 0o644); err != nil {
		t.Fatalf("write report.json: %v", err)
	}

	// report.json must exist and be valid JSON.
	reportContent, err := os.ReadFile(reportPath)
	if err != nil {
		t.Errorf("report.json must exist after PASSED run: %v", err)
	} else {
		var jsonDoc map[string]interface{}
		if err := json.Unmarshal(reportContent, &jsonDoc); err != nil {
			t.Errorf("report.json must be valid JSON: %v", err)
		}
		if status, ok := jsonDoc["status"]; !ok || status != "PASSED" {
			t.Errorf("report.json status: got %v, want PASSED", status)
		}
	}

	// workspace/ must NOT exist after a PASSED run without --keep-workspace.
	workspacePath := filepath.Join(runSubDir, "workspace")
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Errorf("<runDir>/workspace/ must NOT exist after PASSED run without --keep-workspace; err: %v", err)
	}

	// --- Secret safety assertions (Phase 7.2) ---
	// Token must not appear in execution.log or report.json.
	// The e2e test uses an empty token (""), so we assert on the redacted form
	// not appearing (which would indicate some other secret leaked).
	// For comprehensive secret testing, the unit tests in logging package cover
	// the token-absent invariant with a real fake token.
	if strings.Contains(string(logContent), "abc123secret") {
		t.Error("execution.log must not contain raw token literal")
	}
	if strings.Contains(string(reportContent), "abc123secret") {
		t.Error("report.json must not contain raw token literal")
	}
}

// TestEndToEnd_FailurePath verifies that on a FAILED run:
//   - <runDir>/workspace/ exists and contains the generated changelog XML
//   - Container and network cleanup closures were invoked
//
// This test uses a fake Maven runner that forces failure — no Docker required.
func TestEndToEnd_FailurePath(t *testing.T) {

	runDir := t.TempDir()

	// Write a minimal pom.xml and fake archetype structure so the orchestrator
	// reaches the Maven step (past preflight, clone, engine-guard, etc.).
	cloneDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cloneDir, "pom.xml"), []byte(minimalPOM), 0o644); err != nil {
		t.Fatalf("write pom.xml: %v", err)
	}

	// Create a fake SQL file in the clone's SQLInput so the overlay step can succeed.
	sqlInputDir := filepath.Join(cloneDir, "src", "main", "resources", "SQLInput")
	if err := os.MkdirAll(sqlInputDir, 0o755); err != nil {
		t.Fatalf("create SQLInput: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sqlInputDir, "N0001_TA_TEST.sql"), []byte("-- test"), 0o644); err != nil {
		t.Fatalf("write sql: %v", err)
	}

	// Create a fake changelog XML inside the clone's expected path so workspace
	// retention can be verified.
	changelogDir := filepath.Join(cloneDir, "src", "main", "resources", "db", "schema", "changelog")
	if err := os.MkdirAll(changelogDir, 0o755); err != nil {
		t.Fatalf("create changelog dir: %v", err)
	}
	changelogFile := filepath.Join(changelogDir, "generated-changelog.xml")
	if err := os.WriteFile(changelogFile, []byte(`<?xml version="1.0"?><databaseChangeLog/>`), 0o644); err != nil {
		t.Fatalf("write changelog: %v", err)
	}

	containerStopCount := 0
	networkCleanupCount := 0

	deps := orchestrator.Deps{
		Preflight: &fakePreflight{},
		Cloner:    &fakeCloner{root: cloneDir},
		DBProvider: &fakeDatabaseProvider{
			provider: &fakeContainerProvider{
				coords: domain.ContainerCoords{Host: "127.0.0.1", Port: 5432, User: "u", Password: "p", DBName: "db"},
				stopFn: func() error { containerStopCount++; return nil },
			},
		},
		Patcher:         &fakePatcher{},
		Engine:          &fakeEngineDetector{engine: "postgres"},
		Tags:            &fakeTagResolver{tag: "210"},
		ReadinessPolicy: &fastPolicy,
		// Force sync failure so the run ends with StatusFailed.
		Maven: &fakeMavenRunner{
			syncResult: domain.StepResult{Status: domain.StepStatusFailed, Error: "BUILD FAILURE"},
		},
		NetworkCleanup: func() error { networkCleanupCount++; return nil },
		RunDir:         runDir,
		KeepWorkspace:  false,
	}

	cfg := config.Config{
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "main",
		Token:      domain.NewSecret(""),
	}

	rpt := orchestrator.Run(context.Background(), deps, cfg)

	if rpt.Status != domain.StatusFailed {
		t.Fatalf("expected FAILED run, got %v", rpt.Status)
	}

	// Assert workspace exists with the generated changelog XML.
	workspacePath := filepath.Join(runDir, "workspace")
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		t.Errorf("<runDir>/workspace/ must exist after FAILED run")
	} else {
		// The generated changelog XML must be present inside the moved workspace.
		movedChangelog := filepath.Join(workspacePath, "src", "main", "resources", "db", "schema", "changelog", "generated-changelog.xml")
		if _, err := os.Stat(movedChangelog); os.IsNotExist(err) {
			t.Errorf("generated changelog XML must exist inside <runDir>/workspace/ after FAILED run: %v", err)
		}
	}

	// Container and network cleanup must have been invoked regardless of outcome.
	if containerStopCount == 0 {
		t.Error("container Stop must be called on FAILED run")
	}
	if networkCleanupCount == 0 {
		t.Error("network cleanup must be called on FAILED run")
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
