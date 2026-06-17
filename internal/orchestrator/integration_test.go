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
	"github.com/dbflow-validator/dbflow-validator/internal/engine"
	internalgit "github.com/dbflow-validator/dbflow-validator/internal/git"
	"github.com/dbflow-validator/dbflow-validator/internal/liquibase"
	"github.com/dbflow-validator/dbflow-validator/internal/maven"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
	"github.com/dbflow-validator/dbflow-validator/internal/preflight"
	internalvendor "github.com/dbflow-validator/dbflow-validator/internal/vendor"
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

	// Copy the archetype to a temp dir so the flow can mutate liquibase.properties.
	tmpArchetype := t.TempDir()
	t.Logf("copying archetype to %s", tmpArchetype)
	if err := copyDir(archetypeSrc, tmpArchetype); err != nil {
		t.Fatalf("copy archetype: %v", err)
	}

	// Wire real adapters.
	pgContainerProvider := container.NewPostgresProvider()
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

	// Resolve vendored Maven settings.xml from project root.
	// The test binary runs from internal/orchestrator/ which is 2 levels deep
	// inside the project root (dbflow-validator/). Go up 2 levels, not 3.
	projectRoot := filepath.Join("..", "..")
	projectRootAbs, _ := filepath.Abs(projectRoot)
	mavenSettingsPath := ""
	if repoPath, err := internalvendor.FindVendorRepository(projectRootAbs); err == nil {
		settingsDir := t.TempDir()
		if sp, err := internalvendor.WriteSettingsXML(settingsDir, repoPath); err == nil {
			mavenSettingsPath = sp
			t.Logf("using vendored Maven settings: %s", mavenSettingsPath)
		}
	} else {
		t.Logf("mvn-vendor/repository not found (%v); Maven will use ~/.m2", err)
	}

	deps := orchestrator.Deps{
		Preflight:  preflight.New(nil),
		Cloner:     fakeCloner,
		DBProvider: realDBProvider,
		Patcher:    liquibase.NewPatcher(),
		Engine:     engine.NewDetector(),
		Tags:       &liquibase.ChangelogResolver{},
		Maven:      maven.NewRunner("", mavenSettingsPath),
	}

	cfg := config.Config{
		RepoURL:    "local://db-artifacts-scgolfcore",
		BaseBranch: "integracion",
		Token:      domain.NewSecret(""),
	}

	t.Log("Starting end-to-end validation run...")
	rpt := orchestrator.Run(context.Background(), deps, cfg)

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
