package orchestrator_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	// capturedWriter captures the io.Writer passed to Run, for assertion in tests.
	capturedWriter io.Writer
}

func (f *fakeMavenRunner) Run(
	_ context.Context, _ string, goal string, _ []string, out io.Writer,
) (domain.StepResult, error) {
	f.capturedWriter = out
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

// ---- PreSyncValidator seam tests ----

// fakePreSyncValidator is a controllable no-op or error-returning validator.
type fakePreSyncValidator struct {
	err error
}

func (f *fakePreSyncValidator) ValidatePreSync(ctx context.Context, cloneRoot string) error {
	return f.err
}

// TestOrchestrator_PreSyncValidator_NoOp verifies that a nil PreSyncValidator
// (no-op default) does not break the happy path.
func TestOrchestrator_PreSyncValidator_NoOp(t *testing.T) {
	deps := happyDeps(t)
	// Explicitly set nil — should use the no-op default.
	deps.PreSyncValidator = nil

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED with nil PreSyncValidator, got %v; steps: %+v", report.Status, report.Steps)
	}
}

// TestOrchestrator_PreSyncValidator_PassThrough verifies that a no-error validator
// is called without disrupting the pipeline.
func TestOrchestrator_PreSyncValidator_PassThrough(t *testing.T) {
	deps := happyDeps(t)
	deps.PreSyncValidator = &fakePreSyncValidator{err: nil}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED, got %v; steps: %+v", report.Status, report.Steps)
	}
}

// TestOrchestrator_PreSyncValidator_Failure verifies that a failing validator
// aborts the pipeline before dbflow:sync is attempted.
func TestOrchestrator_PreSyncValidator_Failure(t *testing.T) {
	deps := happyDeps(t)
	deps.PreSyncValidator = &fakePreSyncValidator{err: errors.New("SQL rules violation: missing rollback")}

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusFailed {
		t.Errorf("expected FAILED, got %v", report.Status)
	}
	// The failure step name must identify the pre-sync validation step.
	var preValidStep *domain.StepResult
	for i := range report.Steps {
		if report.Steps[i].Name == "pre-sync-validate" {
			preValidStep = &report.Steps[i]
			break
		}
	}
	if preValidStep == nil {
		t.Error("expected step 'pre-sync-validate' in report, not found")
	} else if preValidStep.Status != domain.StepStatusFailed {
		t.Errorf("expected pre-sync-validate FAILED, got %v", preValidStep.Status)
	}
}

// ---- Overlay step tests (Phase 5) ----

// fakeOverlayer records calls and returns a controllable result.
type fakeOverlayer struct {
	called bool
	copied int
	err    error
}

func (f *fakeOverlayer) Apply(_, _ string) (int, error) {
	f.called = true
	return f.copied, f.err
}

// makeHappyDepsWithSQLInput creates happyDeps pre-populated with a temp SQLInput dir
// containing at least one .sql file, so the fail-fast guard passes.
func makeHappyDepsWithSQLInput(t *testing.T) (orchestrator.Deps, config.Config, string) {
	t.Helper()
	deps := happyDeps(t)
	sqlDir := t.TempDir()
	// Write a dummy .sql file so the input-check guard passes.
	if err := os.WriteFile(filepath.Join(sqlDir, "dummy.sql"), []byte("-- dummy"), 0o600); err != nil {
		t.Fatalf("write dummy sql: %v", err)
	}
	cfg := testCfg()
	cfg.SQLInputPath = sqlDir
	return deps, cfg, sqlDir
}

// TestRun_OverlayStep_Wired verifies that when deps.Overlayer is set, the "overlay"
// step appears in Steps between "engine-guard" and "container-start".
func TestRun_OverlayStep_Wired(t *testing.T) {
	deps, cfg, _ := makeHappyDepsWithSQLInput(t)
	ol := &fakeOverlayer{copied: 1}
	deps.Overlayer = ol

	report := orchestrator.Run(context.Background(), deps, cfg)

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED, got %v; steps: %+v", report.Status, report.Steps)
	}
	if !ol.called {
		t.Error("expected Overlayer.Apply to be called")
	}

	// Find step positions.
	engineGuardIdx := -1
	overlayIdx := -1
	containerStartIdx := -1
	for i, s := range report.Steps {
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
		t.Fatal("expected 'overlay' step in report, not found")
	}
	if engineGuardIdx == -1 || containerStartIdx == -1 {
		t.Fatalf("expected 'engine-guard' and 'container-start' steps; indices: %d, %d", engineGuardIdx, containerStartIdx)
	}
	if overlayIdx <= engineGuardIdx {
		t.Errorf("overlay step (%d) must come AFTER engine-guard (%d)", overlayIdx, engineGuardIdx)
	}
	if overlayIdx >= containerStartIdx {
		t.Errorf("overlay step (%d) must come BEFORE container-start (%d)", overlayIdx, containerStartIdx)
	}
	// Overlay step must be PASSED.
	if report.Steps[overlayIdx].Status != domain.StepStatusPassed {
		t.Errorf("expected overlay PASSED, got %v", report.Steps[overlayIdx].Status)
	}
}

// TestRun_OverlayStep_Nil_NoOp verifies that when deps.Overlayer is nil,
// no "overlay" step appears and the run completes normally.
func TestRun_OverlayStep_Nil_NoOp(t *testing.T) {
	deps, cfg, _ := makeHappyDepsWithSQLInput(t)
	deps.Overlayer = nil // explicitly nil

	report := orchestrator.Run(context.Background(), deps, cfg)

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED with nil Overlayer, got %v; steps: %+v", report.Status, report.Steps)
	}
	for _, s := range report.Steps {
		if s.Name == "overlay" {
			t.Error("no 'overlay' step should appear when deps.Overlayer is nil")
		}
	}
}

// ---- MavenOut routing tests ----

// TestOrchestrator_MavenOut_WiredToMaven verifies that when deps.MavenOut is set,
// it is passed as the io.Writer to the Maven runner (instead of io.Discard).
func TestOrchestrator_MavenOut_WiredToMaven(t *testing.T) {
	deps := happyDeps(t)
	var mavenOutBuf bytes.Buffer
	deps.MavenOut = &mavenOutBuf

	fakeMvn := &fakeMavenRunner{
		syncResult:     domain.StepResult{Status: domain.StepStatusPassed},
		rollbackResult: domain.StepResult{Status: domain.StepStatusPassed},
	}
	deps.Maven = fakeMvn

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED, got %v", report.Status)
	}
	// The captured writer passed to Maven.Run should be the same pointer as MavenOut.
	if fakeMvn.capturedWriter == nil {
		t.Error("expected Maven.Run to receive a non-nil io.Writer (deps.MavenOut)")
	}
}

// TestOrchestrator_MavenOut_NilFallsToDiscard verifies that when deps.MavenOut is nil,
// Maven runs normally (falling back to io.Discard) — backward compatibility.
func TestOrchestrator_MavenOut_NilFallsToDiscard(t *testing.T) {
	deps := happyDeps(t)
	deps.MavenOut = nil // default — no explicit MavenOut

	report := orchestrator.Run(context.Background(), deps, testCfg())

	if report.Status != domain.StatusPassed {
		t.Errorf("expected PASSED with nil MavenOut, got %v; steps: %+v", report.Status, report.Steps)
	}
}

// ---- Fail-fast guard tests (Phase 3) ----

// mockCloner tracks whether Clone was called — used to assert the cloner was NOT invoked.
type mockCloner struct {
	called bool
	root   string
	err    error
}

func (m *mockCloner) Clone(_ context.Context, _ domain.CloneOptions) (string, error) {
	m.called = true
	return m.root, m.err
}

// TestRun_InputCheck_MissingSQLInput verifies that when cfg.SQLInputPath does not
// exist on disk, the orchestrator fails early with step "input-check" and never calls Clone.
func TestRun_InputCheck_MissingSQLInput(t *testing.T) {
	cloner := &mockCloner{}
	deps := happyDeps(t)
	deps.Cloner = cloner

	cfg := testCfg()
	cfg.SQLInputPath = "/nonexistent/path/that/does/not/exist/SQLInput"

	report := orchestrator.Run(context.Background(), deps, cfg)

	if report.Status != domain.StatusUsageError {
		t.Errorf("expected USAGE_ERROR, got %v", report.Status)
	}
	if cloner.called {
		t.Error("Clone must NOT be called when SQLInput guard fails")
	}

	// Find input-check step.
	var inputStep *domain.StepResult
	for i := range report.Steps {
		if report.Steps[i].Name == "input-check" {
			inputStep = &report.Steps[i]
			break
		}
	}
	if inputStep == nil {
		t.Fatal("expected step 'input-check' in report, not found")
	}
	if inputStep.Status != domain.StepStatusFailed {
		t.Errorf("expected input-check FAILED, got %v", inputStep.Status)
	}
	if inputStep.Error == "" {
		t.Error("input-check step must have a non-empty error message")
	}
	// Error must contain "nothing to validate".
	if !strings.Contains(inputStep.Error, "nothing to validate") {
		t.Errorf("error should contain 'nothing to validate', got: %q", inputStep.Error)
	}
}

// TestRun_InputCheck_EmptyDir verifies that when cfg.SQLInputPath exists but contains
// no .sql files, the orchestrator fails early with step "input-check".
func TestRun_InputCheck_EmptyDir(t *testing.T) {
	emptyDir := t.TempDir() // exists but contains no files

	cloner := &mockCloner{}
	deps := happyDeps(t)
	deps.Cloner = cloner

	cfg := testCfg()
	cfg.SQLInputPath = emptyDir

	report := orchestrator.Run(context.Background(), deps, cfg)

	if report.Status != domain.StatusUsageError {
		t.Errorf("expected USAGE_ERROR, got %v", report.Status)
	}
	if cloner.called {
		t.Error("Clone must NOT be called when SQLInput dir is empty")
	}

	var inputStep *domain.StepResult
	for i := range report.Steps {
		if report.Steps[i].Name == "input-check" {
			inputStep = &report.Steps[i]
			break
		}
	}
	if inputStep == nil {
		t.Fatal("expected step 'input-check' in report, not found")
	}
	if inputStep.Status != domain.StepStatusFailed {
		t.Errorf("expected input-check FAILED, got %v", inputStep.Status)
	}
	if !strings.Contains(inputStep.Error, "nothing to validate") {
		t.Errorf("error should contain 'nothing to validate', got: %q", inputStep.Error)
	}
	// Must not contain Maven output.
	if strings.Contains(inputStep.Error, "BUILD FAILURE") {
		t.Error("error must not contain Maven BUILD FAILURE output")
	}
}
