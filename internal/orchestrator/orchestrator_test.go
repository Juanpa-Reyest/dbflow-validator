package orchestrator_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/config"
	"github.com/dbflow-validator/dbflow-validator/internal/container"
	"github.com/dbflow-validator/dbflow-validator/internal/domain"
	"github.com/dbflow-validator/dbflow-validator/internal/orchestrator"
)

// minimalPOM is a pom.xml that contains the dbflow plugin (target of driver injection).
// This allows the orchestrator's pom-driver-inject step to succeed on the fake clone dir.
const minimalPOM = `<?xml version="1.0" encoding="UTF-8"?>
<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>test</groupId>
  <artifactId>test</artifactId>
  <version>0.0.1</version>
  <build>
    <plugins>
      <plugin>
        <groupId>com.gs.ftt.coe-ds</groupId>
        <artifactId>relational-db-release-manager-plugin</artifactId>
        <version>0.0.1</version>
      </plugin>
    </plugins>
  </build>
</project>`

// ---- Fake port implementations ----

type fakePreflight struct{ err error }

func (f *fakePreflight) Check(_ context.Context) ([]domain.ToolStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []domain.ToolStatus{
		{Name: "docker", Found: true},
		{Name: "mvn", Found: true},
		{Name: "git", Found: true},
		{Name: "java", Found: true},
	}, nil
}

type fakeCloner struct {
	root string
	err  error
}

func (f *fakeCloner) Clone(_ context.Context, _ domain.CloneOptions) (string, error) {
	return f.root, f.err
}

type fakeContainerProvider struct {
	coords domain.ContainerCoords
	err    error
	stopFn func() error
}

func (f *fakeContainerProvider) Start(_ context.Context) (domain.ContainerCoords, error) {
	return f.coords, f.err
}

func (f *fakeContainerProvider) Stop(_ context.Context) error {
	if f.stopFn != nil {
		return f.stopFn()
	}
	return nil
}

type fakeDatabaseProvider struct {
	provider domain.ContainerProvider
	pingErr  error
}

func (f *fakeDatabaseProvider) Image() string                                      { return "postgres:17.4" }
func (f *fakeDatabaseProvider) ContainerProvider() domain.ContainerProvider        { return f.provider }
func (f *fakeDatabaseProvider) DSN(coords domain.ContainerCoords) string           { return "postgres://fake" }
func (f *fakeDatabaseProvider) Ping(_ context.Context, _ string) error             { return f.pingErr }

type fakePatcher struct{ err error }

func (f *fakePatcher) Patch(_ string, _ domain.ContainerCoords) error { return f.err }

type fakeEngineDetector struct {
	engine string
	err    error
}

func (f *fakeEngineDetector) Detect(_ string) (string, error) { return f.engine, f.err }

type fakeTagResolver struct {
	tag string
	err error
}

func (f *fakeTagResolver) FirstTag(_ string) (string, error) { return f.tag, f.err }

type fakeMavenRunner struct {
	syncResult     domain.StepResult
	syncErr        error
	rollbackResult domain.StepResult
	rollbackErr    error
}

func (f *fakeMavenRunner) Run(
	_ context.Context, _ string, goal string, _ []string, _ io.Writer,
) (domain.StepResult, error) {
	if goal == "dbflow:sync" {
		return f.syncResult, f.syncErr
	}
	return f.rollbackResult, f.rollbackErr
}

// ---- Helpers ----

// fastPolicy is a readiness policy with a very short deadline for unit tests.
var fastPolicy = container.RetryPolicy{
	InitialInterval: 1 * time.Millisecond,
	Multiplier:      1.0,
	MaxInterval:     1 * time.Millisecond,
	Deadline:        10 * time.Millisecond,
}

func happyDeps(t *testing.T) orchestrator.Deps {
	t.Helper()
	cloneDir := t.TempDir()
	// The orchestrator injects the PostgreSQL driver into cloneDir/pom.xml;
	// write a minimal pom that contains the target plugin so the step succeeds.
	if err := os.WriteFile(filepath.Join(cloneDir, "pom.xml"), []byte(minimalPOM), 0o644); err != nil {
		t.Fatalf("write fake pom.xml: %v", err)
	}
	return orchestrator.Deps{
		Preflight: &fakePreflight{},
		Cloner:    &fakeCloner{root: cloneDir},
		DBProvider: &fakeDatabaseProvider{
			provider: &fakeContainerProvider{
				coords: domain.ContainerCoords{Host: "127.0.0.1", Port: 5432, User: "u", Password: "p", DBName: "db"},
			},
		},
		Patcher:         &fakePatcher{},
		Engine:          &fakeEngineDetector{engine: "postgres"},
		Tags:            &fakeTagResolver{tag: "210"},
		Maven: &fakeMavenRunner{
			syncResult:     domain.StepResult{Status: domain.StepStatusPassed},
			rollbackResult: domain.StepResult{Status: domain.StepStatusPassed},
		},
		ReadinessPolicy: &fastPolicy,
	}
}

func testCfg() config.Config {
	return config.Config{
		RepoURL:    "https://example.com/repo.git",
		BaseBranch: "main",
		Token:      domain.NewSecret("tok"),
	}
}

// ---- Tests ----

func TestOrchestrator_HappyPath(t *testing.T) {
	deps := happyDeps(t)
	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED, got %v; steps: %+v", report.Status, report.Steps)
	}
	if len(report.Steps) == 0 {
		t.Error("expected non-empty steps")
	}
}

func TestOrchestrator_PreflightFailure(t *testing.T) {
	deps := happyDeps(t)
	deps.Preflight = &fakePreflight{err: errors.New("docker not found")}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}

func TestOrchestrator_CloneFailure(t *testing.T) {
	deps := happyDeps(t)
	deps.Cloner = &fakeCloner{err: errors.New("auth failed")}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}

func TestOrchestrator_ContainerFailure(t *testing.T) {
	deps := happyDeps(t)
	deps.DBProvider = &fakeDatabaseProvider{
		provider: &fakeContainerProvider{err: errors.New("docker daemon down")},
	}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}

func TestOrchestrator_EngineUnsupported(t *testing.T) {
	deps := happyDeps(t)
	deps.Engine = &fakeEngineDetector{err: domain.ErrUnsupportedEngine}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}

func TestOrchestrator_SyncFailure(t *testing.T) {
	deps := happyDeps(t)
	deps.Maven = &fakeMavenRunner{
		syncResult: domain.StepResult{Status: domain.StepStatusFailed, Error: "BUILD FAILURE"},
	}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}

func TestOrchestrator_RollbackFailure(t *testing.T) {
	deps := happyDeps(t)
	deps.Maven = &fakeMavenRunner{
		syncResult:     domain.StepResult{Status: domain.StepStatusPassed},
		rollbackResult: domain.StepResult{Status: domain.StepStatusFailed, Error: "rollback failed"},
	}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}

func TestOrchestrator_ReadinessTimeout(t *testing.T) {
	deps := happyDeps(t)
	deps.DBProvider = &fakeDatabaseProvider{
		provider: &fakeContainerProvider{
			coords: domain.ContainerCoords{Host: "127.0.0.1", Port: 5432, User: "u", Password: "p", DBName: "db"},
		},
		pingErr: domain.ErrReadinessTimeout,
	}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
}
