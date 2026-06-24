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

// TestBuildContainerRequest_MountsCloneRootRW asserts that the clone root is
// carried as a typed mount with Source=cloneRoot, Target=/work, ReadOnly=false.
// Typed mounts (hc.Mounts) avoid Windows drive-letter colon ambiguity that
// breaks raw bind-string parsing ("E:\...:container:mode").
func TestBuildContainerRequest_MountsCloneRootRW(t *testing.T) {
	req := buildTestRequest(t)
	for _, m := range req.Mounts {
		if m.Source == testCloneRoot && m.Target == "/work" {
			if m.ReadOnly {
				t.Errorf("clone root mount must be read-write (ReadOnly=false), got ReadOnly=true")
			}
			return
		}
	}
	t.Errorf("Mounts must contain {Source:%q, Target:\"/work\", ReadOnly:false}; mounts=%+v", testCloneRoot, req.Mounts)
}

// TestBuildContainerRequest_MountsJARReadOnly asserts that the JAR host path is
// mounted at /val/validator.jar read-only as a typed mount.
func TestBuildContainerRequest_MountsJARReadOnly(t *testing.T) {
	req := buildTestRequest(t)
	for _, m := range req.Mounts {
		if m.Source == testJARPath && m.Target == "/val/validator.jar" {
			if !m.ReadOnly {
				t.Errorf("JAR mount must be read-only (ReadOnly=true), got ReadOnly=false")
			}
			return
		}
	}
	t.Errorf("Mounts must contain {Source:%q, Target:\"/val/validator.jar\", ReadOnly:true}; mounts=%+v", testJARPath, req.Mounts)
}

// TestBuildContainerRequest_WindowsPathPassedThrough is the regression guard for
// the Windows bind-mount bug. A Windows-style source path (with a drive-letter
// colon) must be preserved unchanged as the mount Source — never split on its
// drive colon as a raw "host:container:mode" bind string would be.
func TestBuildContainerRequest_WindowsPathPassedThrough(t *testing.T) {
	winCloneRoot := `E:\Users\x\Temp\clone`
	winJARPath := `C:\cache\validator.jar`
	paths := rulesvalidator.Paths{
		RulesetPath:  winCloneRoot + `\src\main\resources\Validator\RulesContracts\validation-rules.yaml`,
		SQLInputPath: winCloneRoot + `\src\main\resources\SQLInput`,
	}
	req := rulesvalidator.BuildContainerRequest(testImage, winJARPath, 0, 0, winCloneRoot, paths)

	foundClone := false
	foundJAR := false
	for _, m := range req.Mounts {
		if m.Source == winCloneRoot {
			foundClone = true
		}
		if m.Source == winJARPath {
			foundJAR = true
		}
	}
	if !foundClone {
		t.Errorf("Windows clone root %q must appear as mount Source unchanged; mounts=%+v", winCloneRoot, req.Mounts)
	}
	if !foundJAR {
		t.Errorf("Windows JAR path %q must appear as mount Source unchanged; mounts=%+v", winJARPath, req.Mounts)
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
