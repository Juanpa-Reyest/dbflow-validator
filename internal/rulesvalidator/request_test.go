package rulesvalidator_test

import (
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

const (
	testImage     = "maven:3.9-eclipse-temurin-21"
	testCloneRoot = "/work-host/clone"
	testJARPath   = "/cache/validator.jar"
	testUID       = 1000
	testGID       = 1000
)

func buildTestRequest(t *testing.T) rulesvalidator.ValidatorContainerRequest {
	t.Helper()
	paths := rulesvalidator.Paths{
		RulesetPath:  testCloneRoot + "/src/main/resources/Validator/RulesContracts/validation-rules.yaml",
		SQLInputPath: testCloneRoot + "/src/main/resources/SQLInput",
	}
	return rulesvalidator.BuildContainerRequest(
		testImage, testJARPath, testUID, testGID, testCloneRoot, paths,
	)
}

func TestBuildContainerRequest_Image(t *testing.T) {
	req := buildTestRequest(t)
	if req.Image != testImage {
		t.Errorf("Image = %q, want %q", req.Image, testImage)
	}
}

func TestBuildContainerRequest_NoNetwork(t *testing.T) {
	req := buildTestRequest(t)
	if len(req.Networks) != 0 {
		t.Errorf("Networks must be empty (no Docker network required), got %v", req.Networks)
	}
}

func TestBuildContainerRequest_CmdContainsJar(t *testing.T) {
	req := buildTestRequest(t)
	joined := strings.Join(req.Cmd, " ")
	if !strings.Contains(joined, "/val/validator.jar") {
		t.Errorf("Cmd missing /val/validator.jar; cmd=%v", req.Cmd)
	}
}

func TestBuildContainerRequest_CmdContainsValidateSubcommand(t *testing.T) {
	req := buildTestRequest(t)
	if len(req.Cmd) < 2 {
		t.Fatalf("Cmd too short: %v", req.Cmd)
	}
	found := false
	for _, arg := range req.Cmd {
		if arg == "validate" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Cmd missing 'validate' subcommand; cmd=%v", req.Cmd)
	}
}

func TestBuildContainerRequest_CmdContainsRulesetFlag(t *testing.T) {
	req := buildTestRequest(t)
	joined := strings.Join(req.Cmd, " ")
	// The container-side ruleset path must use /work prefix.
	if !strings.Contains(joined, "-cf") || !strings.Contains(joined, "/work") {
		t.Errorf("Cmd missing -cf /work/... ruleset flag; cmd=%v", req.Cmd)
	}
}

func TestBuildContainerRequest_CmdContainsSQLInputFlag(t *testing.T) {
	req := buildTestRequest(t)
	joined := strings.Join(req.Cmd, " ")
	if !strings.Contains(joined, "-sp") || !strings.Contains(joined, "/work") {
		t.Errorf("Cmd missing -sp /work/... SQLInput flag; cmd=%v", req.Cmd)
	}
}

// TestBuildContainerRequest_NoBindMounts asserts that the new request has no Mounts field
// at all — all I/O is via copy-in (CopyDirs, JAR Files) and copy-out (CopyFileFromContainer).
// This is the AC-6/AC-17 gate: no TypeBind mount ever appears in the request.
func TestBuildContainerRequest_NoBindMounts(t *testing.T) {
	req := buildTestRequest(t)
	// The request type no longer has a Mounts field. This test verifies the new copy-in shape.
	// AC-6: zero bind mounts. Verified structurally: ValidatorContainerRequest has no Mounts.
	// We assert CopyDirs is used instead.
	if len(req.CopyDirs) == 0 {
		t.Error("CopyDirs must be non-empty; clone must be copied in via CopyDirToContainer")
	}
}

// TestBuildContainerRequest_CopyDirsContainsCloneRoot asserts the clone root is scheduled
// for copy-in at /work via CopyDirs (replacing the old /work bind mount).
func TestBuildContainerRequest_CopyDirsContainsCloneRoot(t *testing.T) {
	req := buildTestRequest(t)
	for _, cd := range req.CopyDirs {
		if cd.HostPath == testCloneRoot && cd.ContainerParent == "/work" {
			return // found
		}
	}
	t.Errorf("CopyDirs must contain {HostPath:%q, ContainerParent:\"/work\"}; got=%+v", testCloneRoot, req.CopyDirs)
}

// TestBuildContainerRequest_JARFields asserts the JAR copy-in fields are set correctly.
func TestBuildContainerRequest_JARFields(t *testing.T) {
	req := buildTestRequest(t)
	if req.JarHostPath != testJARPath {
		t.Errorf("JarHostPath = %q, want %q", req.JarHostPath, testJARPath)
	}
	if req.JarContainerPath != "/val/validator.jar" {
		t.Errorf("JarContainerPath = %q, want /val/validator.jar", req.JarContainerPath)
	}
}

// TestBuildContainerRequest_ReportPaths asserts container and host report paths are set.
func TestBuildContainerRequest_ReportPaths(t *testing.T) {
	req := buildTestRequest(t)
	wantContainer := "/work/src/main/resources/Validator/outputReport/report/json/validation_report.json"
	if req.ReportContainerPath != wantContainer {
		t.Errorf("ReportContainerPath = %q, want %q", req.ReportContainerPath, wantContainer)
	}
	wantHost := testCloneRoot + "/src/main/resources/Validator/outputReport/report/json/validation_report.json"
	if req.ReportHostPath != wantHost {
		t.Errorf("ReportHostPath = %q, want %q", req.ReportHostPath, wantHost)
	}
}

// TestBuildContainerRequest_CmdContainsOutputFlag asserts -output is in the command.
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

// TestBuildContainerRequest_WindowsPathPassedThrough is the regression guard for
// the Windows path bug. Windows-style paths (with a drive-letter colon) must be
// preserved unchanged as JarHostPath and CopyDirs[*].HostPath — never split on
// their drive colon as a raw "host:container:mode" bind string would be.
func TestBuildContainerRequest_WindowsPathPassedThrough(t *testing.T) {
	winCloneRoot := `E:\Users\x\Temp\clone`
	winJARPath := `C:\cache\validator.jar`
	paths := rulesvalidator.Paths{
		RulesetPath:  winCloneRoot + `\src\main\resources\Validator\RulesContracts\validation-rules.yaml`,
		SQLInputPath: winCloneRoot + `\src\main\resources\SQLInput`,
	}
	req := rulesvalidator.BuildContainerRequest(testImage, winJARPath, 0, 0, winCloneRoot, paths)

	if req.JarHostPath != winJARPath {
		t.Errorf("JarHostPath = %q, want %q (Windows path must pass through unchanged)", req.JarHostPath, winJARPath)
	}
	foundClone := false
	for _, cd := range req.CopyDirs {
		if cd.HostPath == winCloneRoot {
			foundClone = true
			break
		}
	}
	if !foundClone {
		t.Errorf("CopyDirs must contain HostPath=%q (Windows path); got=%+v", winCloneRoot, req.CopyDirs)
	}
}

func TestBuildContainerRequest_EnvHOME(t *testing.T) {
	req := buildTestRequest(t)
	if req.Env["HOME"] != "/tmp" {
		t.Errorf("Env[HOME] = %q, want /tmp; env=%v", req.Env["HOME"], req.Env)
	}
}

func TestBuildContainerRequest_ContainerPaths_UseWorkPrefix(t *testing.T) {
	req := buildTestRequest(t)
	joined := strings.Join(req.Cmd, " ")
	// Container paths must NOT contain the host cloneRoot prefix.
	if strings.Contains(joined, testCloneRoot) {
		t.Errorf("Cmd must use /work/... paths, not host cloneRoot; cmd=%v", req.Cmd)
	}
}
