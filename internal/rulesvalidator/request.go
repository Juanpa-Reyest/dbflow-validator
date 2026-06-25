package rulesvalidator

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// CopyDir describes a directory to be copied into the validator container before it starts.
// HostPath is the source on the host; ContainerParent is the parent directory inside the
// container under which the directory will land.
//
// testcontainers-go CopyDirToContainer semantics (v0.42.0):
//
//	CopyDirToContainer(ctx, hostDir, containerParent, mode)
//
// places hostDir at: path.Dir(containerParent) + "/" + filepath.Base(hostDir)
// NOT at containerParent itself. To land hostDir at "/work/<base>", pass
// containerParent = "/work/_" so that Dir("/work/_") = "/work".
//
// ContainerParent is therefore the "sibling anchor" — callers must use a value
// whose parent directory equals the desired destination parent. Use
// cloneContainerParent() to derive the correct value.
type CopyDir struct {
	HostPath        string
	ContainerParent string
}

// ValidatorContainerRequest holds the resolved parameters for running the
// validator JAR container. It is intentionally a plain struct (no testcontainers
// import) so that unit tests can assert request shape without Docker.
//
// No host bind mounts are used. The JAR is copied via CopyFileToContainer before
// Start; directories are copied via CopyDirToContainer before Start.
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
	// JarContainerPath is the container-side path where the JAR is copied before Start.
	// Always /val/validator.jar.
	JarContainerPath string
	// CopyDirs lists host directories to copy into the container before Start.
	// Each entry is copied via CopyDirToContainer(ctx, entry.HostPath, entry.ContainerParent, mode).
	// See the CopyDir doc comment for the landing-rule semantics.
	CopyDirs []CopyDir
	// ProjectRoot is the container-side root path where cloneRoot contents land after
	// CopyDirToContainer. Equals "/work/" + filepath.Base(cloneRoot). The WorkingDir
	// and all in-container paths (-cf, -sp, -output, report copy-out) are relative to this.
	ProjectRoot string
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

// containerWorkParent is the container-side directory under which the clone directory lands.
// CopyDirToContainer places hostDir at Dir(containerParent)+"/"+Base(hostDir), so passing
// "/work/_" as containerParent results in the clone landing at "/work/<Base(hostDir)>".
const containerWorkParent = "/work/_"

// containerJARPath is the fixed container-side path for the validator JAR.
const containerJARPath = "/val/validator.jar"

// containerOutputReportSuffix is the path suffix appended to ProjectRoot to get the
// -output flag value. The JAR writes the JSON report at <outputDir>/report/json/validation_report.json.
const containerOutputReportSuffix = "/src/main/resources/Validator/outputReport"

// containerReportSuffix is the suffix appended to ProjectRoot to get the full container-side
// path to the JSON report file.
const containerReportSuffix = containerOutputReportSuffix + "/report/json/validation_report.json"

// cloneProjectRoot returns the container-side root where cloneRoot will land after
// CopyDirToContainer with ContainerParent = containerWorkParent.
//
// Landing rule: Dir(containerWorkParent) + "/" + Base(cloneRoot)
//             = "/work"               + "/" + Base(cloneRoot)
//             = "/work/<base>"
func cloneProjectRoot(cloneRoot string) string {
	return "/work/" + filepath.Base(cloneRoot)
}

// BuildContainerRequest constructs the container request for a validator run.
//
// Container shape:
//   - Image: the provided image (default: maven:3.9-eclipse-temurin-21).
//   - NO Docker networks (the validator does not touch the database).
//   - Cmd: java -jar /val/validator.jar validate -cf <rules> -sp <sqlinput> -output <outputDir>
//   - JarHostPath/JarContainerPath: JAR copied before Start via CopyFileToContainer.
//   - CopyDirs: whole clone (cloneRoot) → ContainerParent "/work/_" so the clone
//     lands at "/work/<Base(cloneRoot)>" (ProjectRoot).
//   - ReportContainerPath: ProjectRoot + "/src/main/resources/Validator/outputReport/report/json/validation_report.json"
//   - ReportHostPath: ReportPath(cloneRoot) — where the report lands after CopyFileFromContainer.
//   - Env: HOME=/tmp (suppress /root/.m2 permission warning)
//   - User: UID:GID on Linux when UID != 0
//
// Container paths are derived by replacing the cloneRoot prefix with ProjectRoot.
// The -output flag points to ProjectRoot + "/src/main/resources/Validator/outputReport"
// so the report is written inside the container project root and can be copied out after execution.
func BuildContainerRequest(
	image, jarHostPath string,
	uid, gid int,
	cloneRoot string,
	paths Paths,
) ValidatorContainerRequest {
	if image == "" {
		image = "maven:3.9-eclipse-temurin-21"
	}

	// projectRoot is the container-side directory where the clone will land.
	// CopyDirToContainer(ctx, cloneRoot, "/work/_", ...) places the clone at
	// Dir("/work/_") + "/" + Base(cloneRoot) = "/work/" + Base(cloneRoot).
	projectRoot := cloneProjectRoot(cloneRoot)

	// Derive container-side paths by swapping the cloneRoot prefix for projectRoot.
	// filepath.ToSlash ensures forward slashes in container paths.
	containerRulesetPath := toContainerPath(cloneRoot, projectRoot, paths.RulesetPath)
	containerSQLInputPath := toContainerPath(cloneRoot, projectRoot, paths.SQLInputPath)
	containerOutputReportDir := projectRoot + containerOutputReportSuffix
	containerReportPath := projectRoot + containerReportSuffix

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
		// JAR is copied before Start via CopyFileToContainer.
		JarHostPath:      jarHostPath,
		JarContainerPath: containerJARPath,
		// Clone root is copied under /work before Start.
		// ContainerParent = "/work/_" → clone lands at "/work/<Base(cloneRoot)>" = ProjectRoot.
		CopyDirs: []CopyDir{
			{HostPath: cloneRoot, ContainerParent: containerWorkParent},
		},
		ProjectRoot: projectRoot,
		// Report locations.
		ReportContainerPath: containerReportPath,
		ReportHostPath:      ReportPath(cloneRoot),
		Env:                 env,
		User:                user,
	}
}

// toContainerPath converts a host absolute path inside cloneRoot to its
// container-side equivalent under projectRoot.
func toContainerPath(cloneRoot, projectRoot, hostPath string) string {
	rel, err := filepath.Rel(cloneRoot, hostPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		// Fallback: use the original path (should not happen in well-formed input).
		return hostPath
	}
	return projectRoot + "/" + filepath.ToSlash(rel)
}
