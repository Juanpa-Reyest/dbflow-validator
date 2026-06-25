package rulesvalidator

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// CopyDir describes a directory to be copied into the validator container before it starts.
// HostPath is the source on the host; ContainerParent is the destination directory inside the container.
// CopyDirToContainer copies the CONTENTS of HostPath into ContainerParent.
type CopyDir struct {
	HostPath        string
	ContainerParent string
}

// ValidatorContainerRequest holds the resolved parameters for running the
// validator JAR container. It is intentionally a plain struct (no testcontainers
// import) so that unit tests can assert request shape without Docker.
//
// No host bind mounts are used. The JAR is copied via ContainerRequest.Files at
// create time; directories are copied via CopyDirToContainer before Start.
type ValidatorContainerRequest struct {
	// Image is the Docker image, e.g. "maven:3.9-eclipse-temurin-21".
	Image string
	// Networks lists the Docker networks to join. Empty for the validator
	// (it does not need to reach the database).
	Networks []string
	// Cmd is the container entrypoint command.
	Cmd []string
	// JarHostPath is the host-side absolute path to the extracted validator JAR.
	JarHostPath string
	// JarContainerPath is the container-side path where the JAR is copied at create time.
	// Always /val/validator.jar.
	JarContainerPath string
	// CopyDirs lists host directories to copy into the container before Start.
	// Each entry is copied via CopyDirToContainer(ctx, entry.HostPath, entry.ContainerParent, mode).
	CopyDirs []CopyDir
	// ReportContainerPath is the container-side path of the JSON report written by the JAR.
	ReportContainerPath string
	// ReportHostPath is the host-side path where the report is written after CopyFileFromContainer.
	// Equals ReportPath(cloneRoot). Written before ReadReportFile so the retained clone contains evidence.
	ReportHostPath string
	// Env holds additional environment variables for the container.
	Env map[string]string
	// User is the "UID:GID" string for --user flag (empty on non-Linux or root).
	User string
}

// containerWorkDir is the directory where cloneRoot contents are copied inside the container.
const containerWorkDir = "/work"

// containerJARPath is the fixed container-side path for the validator JAR.
const containerJARPath = "/val/validator.jar"

// containerOutputReportDir is the container-side path for the -output flag.
// The JAR writes the JSON report at <containerOutputReportDir>/report/json/validation_report.json.
const containerOutputReportDir = containerWorkDir + "/src/main/resources/Validator/outputReport"

// containerReportPath is the full container-side path to the JSON report file.
const containerReportPath = containerOutputReportDir + "/report/json/validation_report.json"

// BuildContainerRequest constructs the container request for a validator run.
//
// Container shape:
//   - Image: the provided image (default: maven:3.9-eclipse-temurin-21).
//   - NO Docker networks (the validator does not touch the database).
//   - Cmd: java -jar /val/validator.jar validate -cf <rules> -sp <sqlinput> -output <outputDir>
//   - JarHostPath/JarContainerPath: JAR copied at create time via ContainerRequest.Files.
//   - CopyDirs: whole clone (cloneRoot) copied to /work before Start.
//   - ReportContainerPath: /work/src/main/resources/Validator/outputReport/report/json/validation_report.json
//   - ReportHostPath: ReportPath(cloneRoot) — where the report lands after CopyFileFromContainer.
//   - Env: HOME=/tmp (suppress /root/.m2 permission warning)
//   - User: UID:GID on Linux when UID != 0
//
// Container paths are derived by replacing the cloneRoot prefix with /work.
// The -output flag points to <containerWorkDir>/src/main/resources/Validator/outputReport
// so the report is written inside /work and can be copied out after execution.
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
		// JAR is copied at container-create time via ContainerRequest.Files.
		JarHostPath:      jarHostPath,
		JarContainerPath: containerJARPath,
		// Clone root is copied into /work before Start.
		CopyDirs: []CopyDir{
			{HostPath: cloneRoot, ContainerParent: containerWorkDir},
		},
		// Report locations.
		ReportContainerPath: containerReportPath,
		ReportHostPath:      ReportPath(cloneRoot),
		Env:                 env,
		User:                user,
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
