package rulesvalidator_test

import (
	"strings"
	"testing"

	"github.com/dbflow-validator/dbflow-validator/internal/rulesvalidator"
)

const (
	testImage      = "maven:3.9-eclipse-temurin-21"
	testCloneRoot  = "/work-host/clone"
	testJARPath    = "/cache/validator.jar"
	testUID        = 1000
	testGID        = 1000
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

func TestBuildContainerRequest_BindsContainCloneRootMount(t *testing.T) {
	req := buildTestRequest(t)
	found := false
	for _, b := range req.Binds {
		if strings.HasPrefix(b, testCloneRoot+":") && strings.Contains(b, "/work") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Binds must include cloneRoot:/work mount; binds=%v", req.Binds)
	}
}

func TestBuildContainerRequest_BindsContainJARMount(t *testing.T) {
	req := buildTestRequest(t)
	found := false
	for _, b := range req.Binds {
		if strings.HasPrefix(b, testJARPath+":") && strings.Contains(b, "/val/validator.jar") && strings.HasSuffix(b, ":ro") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Binds must include jarPath:/val/validator.jar:ro; binds=%v", req.Binds)
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
