package maven

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// DefaultImage is the Maven container image used when no image is explicitly specified.
const DefaultImage = "maven:3.9-eclipse-temurin-21"

// ContainerRunner executes Maven goals inside a Docker container.
// This replaces host-exec Maven invocations: the host does NOT need mvn or a JVM.
//
// Architecture:
//   - The container joins a shared Docker network (same as the Postgres container).
//   - The archetype clone directory is copied INTO the container at /work via CopyDirToContainer.
//   - The vendored Maven repo is copied INTO the container at /m2 via CopyDirToContainer.
//   - No host bind mounts are used — Docker Desktop file-sharing prompts never appear.
//   - Maven is invoked as: mvn -f /work/pom.xml -B -s /work/settings.xml
//     -Dmaven.repo.local=/m2 <goal> -Dparams=<...>
//   - stdout/stderr are streamed live and captured for the trace.
//   - Container exit code and BUILD FAILURE stdout scan map to domain.StepResult.
type ContainerRunner struct {
	image        string
	networkName  string
	repoCachePath string // host path to the vendored Maven repo; mounted at /m2 (ro)
	uid          int
	gid          int
}

// NewContainerRunner creates a ContainerRunner.
//
// Parameters:
//   - image:         Docker image, e.g. "maven:3.9-eclipse-temurin-21" (DefaultImage).
//   - networkName:   Name of the Docker network to join. May be empty at construction
//                    time and set later via SetNetworkName when the network is created
//                    lazily (e.g. at container-start time in the orchestrator).
//   - repoCachePath: Absolute host path to the vendored Maven repo; mounted RO at /m2.
//   - uid, gid:      Host UID and GID for --user flag (pass os.Getuid/Getgid on Linux;
//                    pass 0 to skip --user on non-Linux or for root).
func NewContainerRunner(image, networkName, repoCachePath string, uid, gid int) *ContainerRunner {
	if image == "" {
		image = DefaultImage
	}
	return &ContainerRunner{
		image:         image,
		networkName:   networkName,
		repoCachePath: repoCachePath,
		uid:           uid,
		gid:           gid,
	}
}

// SetNetworkName updates the Docker network that the Maven container will join on the
// next Run call. Use this when the network is created lazily (after construction).
// Not safe for concurrent use — call before the first Run.
func (r *ContainerRunner) SetNetworkName(name string) {
	r.networkName = name
}

// BuildContainerRequest constructs the testcontainers.ContainerRequest for a Maven run.
// This is exported so that unit tests can inspect the request structure (e.g. assert
// that HOME=/tmp is set to prevent the /root/.m2 permission warning).
//
// The request sets:
//   - Image, Networks, Cmd (mvn invocation). No host bind mounts.
//   - Env["HOME"] = "/tmp" so Maven writes .m2 to /tmp instead of /root/.m2.
//     This suppresses "mkdir: cannot create directory '/root': Permission denied"
//     when the container runs as a non-root host UID (via --user UID:GID).
//   - ConfigModifier: WorkingDir=cloneProjectRoot, and --user UID:GID on Linux (non-root).
//   - Started: false — caller must CopyDirToContainer then c.Start manually.
//
// CopyDirToContainer landing rule (testcontainers-go v0.42.0):
//
//	CopyDirToContainer(ctx, hostDir, containerParent, mode) places hostDir at
//	path.Dir(containerParent) + "/" + filepath.Base(hostDir), NOT at containerParent.
//
// To land cloneRoot at "/work/<Base(cloneRoot)>", Run passes "/work/_" as containerParent.
// To land repoCachePath at "/m2/<Base(repoCachePath)>", Run passes "/m2/_" as containerParent.
// BuildContainerRequest computes those derived paths and uses them in the cmd and WorkingDir.
func BuildContainerRequest(
	image, networkName, repoCachePath string,
	uid, gid int,
	cloneRoot, goal string,
	params []string,
) testcontainers.ContainerRequest {
	// cloneProjectRoot is where CopyDirToContainer lands cloneRoot.
	//
	// CopyDirToContainer(ctx, cloneRoot, "/work/_", mode):
	//   parent = Dir("/work/_") = "/work"   (Docker creates this via ConfigModifier.WorkingDir)
	//   landing = "/work/" + Base(cloneRoot)
	cloneProjectRoot := "/work/" + filepath.Base(cloneRoot)

	// m2Root is where CopyDirToContainer lands repoCachePath.
	//
	// CopyDirToContainer(ctx, repoCachePath, "/tmp/_", mode):
	//   parent = Dir("/tmp/_") = "/tmp"   (/tmp always exists in any container)
	//   landing = "/tmp/" + Base(repoCachePath)
	//
	// When repoCachePath is empty, no copy is done; Maven resolves deps online.
	// The placeholder value here is never used in that case.
	m2Root := "/tmp/m2"
	if repoCachePath != "" {
		m2Root = "/tmp/" + filepath.Base(repoCachePath)
	}

	paramStr := strings.Join(params, " ")
	cmd := []string{
		"mvn",
		"-f", cloneProjectRoot + "/pom.xml",
		"-B",
		"-s", cloneProjectRoot + "/settings.xml",
		"-Dmaven.repo.local=" + m2Root,
		goal,
		"-Dparams=" + paramStr,
	}

	req := testcontainers.ContainerRequest{
		Image:    image,
		Networks: []string{networkName},
		Cmd:      cmd,
		// Set HOME=/tmp so Maven writes .m2 to /tmp instead of /root/.m2.
		// Without this, running as a non-root host UID (--user UID:GID) causes
		// the entrypoint to print "mkdir: cannot create directory '/root': Permission denied"
		// because the image's default $HOME is /root, which is not writable by other UIDs.
		Env: map[string]string{
			"HOME": "/tmp",
		},
		// No HostConfigModifier: no bind mounts. Directories are copied in via
		// CopyDirToContainer after the container is created (see Run).
		// Wait until the Maven container exits before returning from GenericContainer.
		// Timeout is intentionally absent — context cancellation handles deadline.
		WaitingFor: wait.ForExit(),
	}

	// ConfigModifier sets the working directory to cloneProjectRoot (so Java's user.dir is
	// the project root, resolving relative paths correctly) and the --user flag on Linux.
	req.ConfigModifier = func(c *dockercontainer.Config) {
		c.WorkingDir = cloneProjectRoot
		// Set --user on Linux when not running as root.
		if runtime.GOOS == "linux" && uid != 0 {
			c.User = fmt.Sprintf("%d:%d", uid, gid)
		}
	}

	return req
}

// Run executes mvn inside a freshly-created Maven container on the shared Docker network.
//
// The container:
//   - Is CREATED (not started) via GenericContainer with Started:false.
//   - cloneRoot is copied into /work via CopyDirToContainer before the container starts.
//   - repoCachePath is copied into /m2 via CopyDirToContainer (when non-empty).
//   - Container is then started with c.Start(ctx).
//   - No host bind mounts are used — Docker Desktop file-sharing prompts never appear.
//   - Runs as the host UID:GID on Linux so files written into /work are not root-owned.
//   - Sets HOME=/tmp so Maven does not attempt to write to /root/.m2.
//   - Streams stdout/stderr to `out` and captures the full trace.
//   - Is removed when Run returns (Terminate; safe even if Start failed).
//
// Exit-code mapping:
//   - exit 0 AND no "BUILD FAILURE" in stdout → StepStatusPassed
//   - exit 0 AND "BUILD FAILURE" in stdout    → StepStatusFailed
//   - exit != 0                               → StepStatusFailed
//   - ctx cancelled before exit               → StepStatusAborted
func (r *ContainerRunner) Run(
	ctx context.Context,
	cloneRoot string,
	goal string,
	params []string,
	out io.Writer,
) (domain.StepResult, error) {
	start := time.Now()

	// Inject unique TAG if not already present.
	uniqueTag := time.Now().Format(time.RFC3339Nano)
	finalParams := ensureTagStr(params, uniqueTag)

	req := BuildContainerRequest(
		r.image, r.networkName, r.repoCachePath,
		r.uid, r.gid,
		cloneRoot, goal, finalParams,
	)

	// Create the container without starting it (Started:false) so we can copy
	// directories in before execution begins.
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false,
	})
	if err != nil {
		// Check if context was cancelled before the container was even created.
		if ctx.Err() != nil {
			return domain.StepResult{
				Name:     goal,
				Status:   domain.StepStatusAborted,
				Error:    fmt.Sprintf("context cancelled before container create: %v", ctx.Err()),
				Duration: time.Since(start),
			}, nil
		}
		return domain.StepResult{}, fmt.Errorf("create maven container: %w", err)
	}
	// Terminate is safe even on a created-but-not-started container (no-op if not running).
	defer func() { _ = c.Terminate(ctx) }()

	// Copy cloneRoot into the container under /work.
	//
	// CopyDirToContainer landing rule: Dir(containerParent) + "/" + Base(hostDir).
	// Passing "/work/_" as containerParent → Dir("/work/_") = "/work"
	// → clone lands at "/work/" + Base(cloneRoot) = cloneProjectRoot.
	// This matches the path used in the cmd (-f, -s) and ConfigModifier.WorkingDir.
	if err := c.CopyDirToContainer(ctx, cloneRoot, "/work/_", 0o755); err != nil {
		if ctx.Err() != nil {
			return domain.StepResult{
				Name:     goal,
				Status:   domain.StepStatusAborted,
				Error:    fmt.Sprintf("context cancelled during copy-in: %v", ctx.Err()),
				Duration: time.Since(start),
			}, nil
		}
		return domain.StepResult{}, fmt.Errorf("copy clone into maven container: %w", err)
	}

	// Copy vendored Maven repo into the container under /tmp (optional — absent means online resolution).
	//
	// Landing rule: CopyDirToContainer(ctx, repoCachePath, "/tmp/_", mode)
	//   parent = Dir("/tmp/_") = "/tmp"   (/tmp always exists in any container image)
	//   landing = "/tmp/" + Base(repoCachePath) = m2Root used in -Dmaven.repo.local.
	if r.repoCachePath != "" {
		if err := c.CopyDirToContainer(ctx, r.repoCachePath, "/tmp/_", 0o755); err != nil {
			if ctx.Err() != nil {
				return domain.StepResult{
					Name:     goal,
					Status:   domain.StepStatusAborted,
					Error:    fmt.Sprintf("context cancelled during m2 copy-in: %v", ctx.Err()),
					Duration: time.Since(start),
				}, nil
			}
			return domain.StepResult{}, fmt.Errorf("copy Maven repo into container: %w", err)
		}
	}

	// Start the container now that all inputs are present inside it.
	if err := c.Start(ctx); err != nil {
		if ctx.Err() != nil {
			return domain.StepResult{
				Name:     goal,
				Status:   domain.StepStatusAborted,
				Error:    fmt.Sprintf("context cancelled before container start: %v", ctx.Err()),
				Duration: time.Since(start),
			}, nil
		}
		return domain.StepResult{}, fmt.Errorf("start maven container: %w", err)
	}

	// Collect container logs (stdout+stderr combined) for the trace.
	var capture bytes.Buffer
	var mw io.Writer
	if out != nil {
		mw = io.MultiWriter(out, &capture)
	} else {
		mw = &capture
	}

	// Read the container logs (blocking until container exits).
	logs, logsErr := c.Logs(ctx)
	if logsErr != nil && ctx.Err() == nil {
		// Non-fatal: we may still get the exit code.
		fmt.Fprintf(mw, "[log-stream error: %v]\n", logsErr)
	}
	if logs != nil {
		_, _ = io.Copy(mw, logs)
		logs.Close()
	}

	trace := capture.String()
	elapsed := time.Since(start)

	// Inspect the container's exit code.
	exitCode := 0
	state, stateErr := c.State(ctx)
	if stateErr != nil {
		// Cannot determine exit code — treat as failure.
		exitCode = 1
	} else {
		exitCode = state.ExitCode
	}

	return MapContainerResult(exitCode, trace, goal, elapsed, ctx.Err()), nil
}

// MapContainerResult converts the raw container outcome (exit code, captured
// stdout/stderr trace, context error) into a domain.StepResult.
//
// Mapping rules:
//   - ctx cancelled/deadline → StepStatusAborted
//   - exitCode != 0          → StepStatusFailed  (error includes exit code)
//   - "BUILD FAILURE" in trace AND exitCode == 0 → StepStatusFailed
//   - exitCode == 0 AND no BUILD FAILURE          → StepStatusPassed
//
// This function is exported so it can be unit-tested directly without spinning
// up a real Docker container (see container_runner_test.go).
func MapContainerResult(
	exitCode int,
	trace string,
	goal string,
	elapsed time.Duration,
	ctxErr error,
) domain.StepResult {
	result := domain.StepResult{
		Name:       goal,
		Duration:   elapsed,
		DurationMs: elapsed.Milliseconds(),
	}

	if ctxErr != nil {
		result.Status = domain.StepStatusAborted
		result.Error = fmt.Sprintf("context cancelled: %v", ctxErr)
		result.Trace = trace
		return result
	}

	if exitCode != 0 || strings.Contains(trace, "BUILD FAILURE") {
		result.Status = domain.StepStatusFailed
		if exitCode != 0 {
			result.Error = fmt.Sprintf("maven container exited with code %d", exitCode)
		} else {
			result.Error = "BUILD FAILURE detected in Maven output"
		}
		result.Trace = trace
		return result
	}

	result.Status = domain.StepStatusPassed
	result.Trace = trace
	return result
}

// Ensure ContainerRunner satisfies domain.MavenRunner at compile time.
var _ domain.MavenRunner = (*ContainerRunner)(nil)
