package rulesvalidator_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

// ---------------------------------------------------------------------------
// ReadReportFile — unit tests
// ---------------------------------------------------------------------------

func TestReadReportFile_Pass_ReturnsPassStatus(t *testing.T) {
	path := filepath.Join("testdata", "pass_report.json")
	rpt, err := rulesvalidator.ReadReportFile(path)
	if err != nil {
		t.Fatalf("ReadReportFile(pass_report.json): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Status != "PASS" {
		t.Errorf("status = %q, want PASS", rpt.GlobalSummary.Status)
	}
}

func TestReadReportFile_Pass_ScoreIs100(t *testing.T) {
	path := filepath.Join("testdata", "pass_report.json")
	rpt, err := rulesvalidator.ReadReportFile(path)
	if err != nil {
		t.Fatalf("ReadReportFile(pass_report.json): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Score != 100.0 {
		t.Errorf("score = %v, want 100.0", rpt.GlobalSummary.Score)
	}
}

func TestReadReportFile_Fail_ReturnsFailStatus(t *testing.T) {
	path := filepath.Join("testdata", "fail_report.json")
	rpt, err := rulesvalidator.ReadReportFile(path)
	if err != nil {
		t.Fatalf("ReadReportFile(fail_report.json): unexpected error: %v", err)
	}
	if rpt.GlobalSummary.Status != "FAIL" {
		t.Errorf("status = %q, want FAIL", rpt.GlobalSummary.Status)
	}
}

func TestReadReportFile_Fail_HasViolations(t *testing.T) {
	path := filepath.Join("testdata", "fail_report.json")
	rpt, err := rulesvalidator.ReadReportFile(path)
	if err != nil {
		t.Fatalf("ReadReportFile(fail_report.json): unexpected error: %v", err)
	}
	sev := rpt.GlobalSummary.SummaryMetrics.ViolationsBySeverity
	if sev["blocker"] == 0 {
		t.Errorf("expected blocker > 0, got violationsBySeverity=%v", sev)
	}
}

func TestReadReportFile_Fail_HasFileReport(t *testing.T) {
	path := filepath.Join("testdata", "fail_report.json")
	rpt, err := rulesvalidator.ReadReportFile(path)
	if err != nil {
		t.Fatalf("ReadReportFile(fail_report.json): unexpected error: %v", err)
	}
	if len(rpt.FileReport) == 0 {
		t.Fatal("expected at least one file report entry")
	}
}

func TestReadReportFile_MissingFile_ReturnsErrNoReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "validation_report.json")
	_, err := rulesvalidator.ReadReportFile(path)
	if !errors.Is(err, rulesvalidator.ErrNoReport) {
		t.Errorf("ReadReportFile(missing): expected ErrNoReport, got %v", err)
	}
}

func TestReadReportFile_MalformedJSON_ReturnsErrNoReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "validation_report.json")
	if err := os.WriteFile(path, []byte(`{"globalSummary": BROKEN`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	_, err := rulesvalidator.ReadReportFile(path)
	if !errors.Is(err, rulesvalidator.ErrNoReport) {
		t.Errorf("ReadReportFile(malformed): expected ErrNoReport, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ReportPath — derives the host-side path from cloneRoot
// ---------------------------------------------------------------------------

func TestReportPath_ReturnsExpectedSubpath(t *testing.T) {
	cloneRoot := "/some/clone"
	got := rulesvalidator.ReportPath(cloneRoot)
	want := "/some/clone/src/main/resources/Validator/outputReport/report/json/validation_report.json"
	if got != want {
		t.Errorf("ReportPath(%q) = %q, want %q", cloneRoot, got, want)
	}
}

// ---------------------------------------------------------------------------
// request.go: -output flag in Cmd + cloneRoot mounted read-write
// ---------------------------------------------------------------------------

func TestBuildContainerRequest_CmdContainsOutputFlag(t *testing.T) {
	req := buildTestRequest(t)
	joined := strings.Join(req.Cmd, " ")
	if !strings.Contains(joined, "-output") {
		t.Errorf("Cmd must contain -output flag for JSON report; cmd=%v", req.Cmd)
	}
}

func TestBuildContainerRequest_OutputPointsToOutputReportInWork(t *testing.T) {
	req := buildTestRequest(t)
	var outputVal string
	for i, arg := range req.Cmd {
		if arg == "-output" && i+1 < len(req.Cmd) {
			outputVal = req.Cmd[i+1]
			break
		}
	}
	if outputVal == "" {
		t.Fatalf("Cmd missing -output value; cmd=%v", req.Cmd)
	}
	want := "/work/src/main/resources/Validator/outputReport"
	if outputVal != want {
		t.Errorf("-output value = %q, want %q", outputVal, want)
	}
}

func TestBuildContainerRequest_CloneRootMountedReadWrite(t *testing.T) {
	req := buildTestRequest(t)
	// The cloneRoot bind must NOT end in :ro — must be rw so the container can write the report.
	for _, b := range req.Binds {
		if strings.HasPrefix(b, testCloneRoot+":") && strings.Contains(b, "/work") {
			if strings.HasSuffix(b, ":ro") {
				t.Errorf("cloneRoot bind must not be :ro (container must write report); bind=%q", b)
			}
			return
		}
	}
	t.Errorf("cloneRoot:/work bind not found; binds=%v", req.Binds)
}

// ---------------------------------------------------------------------------
// validator.go: file-based flow
//
// fileWritingRunner is a ContainerRunner test double that writes a canned JSON
// report file into the cloneRoot's expected outputReport path, simulating what
// the real JAR does when called with -output.
// ---------------------------------------------------------------------------

type fileWritingRunner struct {
	reportJSON string
	err        error
}

func (f *fileWritingRunner) RunValidator(
	_ context.Context,
	req rulesvalidator.ValidatorContainerRequest,
) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	// Derive the host-side cloneRoot from the binds: the bind that maps to /work.
	// We write the report at the outputReport path within that cloneRoot.
	var cloneRoot string
	for _, b := range req.Binds {
		parts := strings.SplitN(b, ":", 3)
		if len(parts) >= 2 && parts[1] == "/work" {
			cloneRoot = parts[0]
			break
		}
	}
	if cloneRoot == "" {
		return "", errors.New("fileWritingRunner: no /work bind found in request")
	}
	reportPath := rulesvalidator.ReportPath(cloneRoot)
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return "", err
	}
	return "", os.WriteFile(reportPath, []byte(f.reportJSON), 0o644)
}

func fixtureJSON(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("load JSON fixture %q: %v", name, err)
	}
	return string(data)
}

func makeValidatorCloneRootWithOutputReportDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rulesetDir := filepath.Join(root, "src", "main", "resources", "Validator", "RulesContracts")
	if err := os.MkdirAll(rulesetDir, 0o700); err != nil {
		t.Fatalf("mkdir ruleset: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesetDir, "validation-rules.yaml"), []byte("rules: []"), 0o600); err != nil {
		t.Fatalf("write ruleset: %v", err)
	}
	sqlInputDir := filepath.Join(root, "src", "main", "resources", "SQLInput")
	if err := os.MkdirAll(sqlInputDir, 0o700); err != nil {
		t.Fatalf("mkdir SQLInput: %v", err)
	}
	outputReportDir := filepath.Join(root, "src", "main", "resources", "Validator", "outputReport")
	if err := os.MkdirAll(outputReportDir, 0o755); err != nil {
		t.Fatalf("mkdir outputReport: %v", err)
	}
	return root
}

func TestContainerValidator_FileBasedFlow_Pass_ReturnsNil(t *testing.T) {
	passJSON := fixtureJSON(t, "pass_report.json")
	runner := &fileWritingRunner{reportJSON: passJSON}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err != nil {
		t.Errorf("ValidatePreSync(file-based PASS): expected nil, got %v", err)
	}
}

func TestContainerValidator_FileBasedFlow_Fail_ReturnsError(t *testing.T) {
	failJSON := fixtureJSON(t, "fail_report.json")
	runner := &fileWritingRunner{reportJSON: failJSON}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("ValidatePreSync(file-based FAIL): expected non-nil error")
	}
}

func TestContainerValidator_FileBasedFlow_MissingReport_ReturnsError(t *testing.T) {
	// Runner succeeds but writes nothing — report file absent → fail-closed.
	runner := &fileWritingRunner{reportJSON: ""}

	v := rulesvalidator.New(
		"maven:3.9-eclipse-temurin-21",
		"/cache/validator.jar",
		1000, 1000,
		runner,
	)

	cloneRoot := makeValidatorCloneRootWithOutputReportDir(t)
	err := v.ValidatePreSync(context.Background(), cloneRoot)
	if err == nil {
		t.Error("ValidatePreSync(no report file): expected fail-closed error, got nil")
	}
}
