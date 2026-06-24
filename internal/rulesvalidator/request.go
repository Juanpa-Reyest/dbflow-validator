package rulesvalidator

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// ValidatorContainerRequest holds the resolved parameters for running the
// validator JAR container.  It is intentionally a plain struct (no testcontainers
// import) so that unit tests can assert request shape without Docker.
type ValidatorContainerRequest struct {
	// Image is the Docker image, e.g. "maven:3.9-eclipse-temurin-21".
	Image string
	// Networks lists the Docker networks to join.  Empty for the validator
	// (it does not need to reach the database).
	Networks []string
	// Cmd is the container entrypoint command.
	Cmd []string
	// Binds is the list of volume bind mounts in "host:container[:options]" format.
	Binds []string
	// Env holds additional environment variables for the container.
	Env map[string]string
	// User is the "UID:GID" string for --user flag (empty on non-Linux or root).
	User string
}

// containerWorkDir is the directory where cloneRoot is mounted inside the container.
const containerWorkDir = "/work"

// containerJARPath is the fixed container-side path for the validator JAR.
const containerJARPath = "/val/validator.jar"

// containerOutputReportDir is the container-side path for the -output flag.
// The JAR writes the JSON report at <containerOutputReportDir>/report/json/validation_report.json.
// This path must be under /work (the cloneRoot mount) so the report lands on the host filesystem
// and the workspace-retention logic can expose it as evidence on failure.
const containerOutputReportDir = containerWorkDir + "/src/main/resources/Validator/outputReport"

// BuildContainerRequest constructs the container request for a validator run.
//
// Container shape:
//   - Image: the provided image (default: maven:3.9-eclipse-temurin-21).
//   - NO Docker networks (the validator does not touch the database).
//   - Cmd: java -jar /val/validator.jar validate -cf <rules> -sp <sqlinput> -output <outputDir>
//   - Binds: cloneRoot:/work:rw (rw so the container can write the JSON report), jarHostPath:/val/validator.jar:ro
//   - Env: HOME=/tmp (suppress /root/.m2 permission warning)
//   - User: UID:GID on Linux when UID != 0
//
// Container paths are derived by replacing the cloneRoot prefix with /work.
// The -output flag points to <containerWorkDir>/src/main/resources/Validator/outputReport
// so the report is written inside the clone and retained as evidence on failure.
func BuildContainerRequest(
	image, jarHostPath string,
	uid, gid int,
	cloneRoot string,
	paths Paths,
) ValidatorContainerRequest {
	if image == "" {
		image = "maven:3.9-eclipse-temurin-21"
	}

	// Derive container-side paths by swapping cloneRoot prefix for /work.
	// filepath.ToSlash ensures forward slashes in container paths.
	containerRulesetPath := toContainerPath(cloneRoot, paths.RulesetPath)
	containerSQLInputPath := toContainerPath(cloneRoot, paths.SQLInputPath)

	cmd := []string{
		"java", "-jar", containerJARPath,
		"validate",
		"-cf", containerRulesetPath,
		"-sp", containerSQLInputPath,
		"-output", containerOutputReportDir,
	}

	// cloneRoot is mounted read-write so the JAR can write the JSON report inside the clone.
	binds := []string{
		cloneRoot + ":" + containerWorkDir + ":rw",
		jarHostPath + ":" + containerJARPath + ":ro",
	}

	env := map[string]string{
		"HOME": "/tmp",
	}

	user := ""
	if runtime.GOOS == "linux" && uid != 0 {
		user = fmt.Sprintf("%d:%d", uid, gid)
	}

	return ValidatorContainerRequest{
		Image:    image,
		Networks: nil, // no Docker network required
		Cmd:      cmd,
		Binds:    binds,
		Env:      env,
		User:     user,
	}
}

// toContainerPath converts a host absolute path inside cloneRoot to its
// container-side equivalent under /work.
func toContainerPath(cloneRoot, hostPath string) string {
	rel, err := filepath.Rel(cloneRoot, hostPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		// Fallback: use the original path (should not happen in well-formed input).
		return hostPath
	}
	return containerWorkDir + "/" + filepath.ToSlash(rel)
}
