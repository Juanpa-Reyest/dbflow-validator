package maven

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// DefaultImage is the Maven container image used when no image is explicitly specified.
const DefaultImage = "maven:3.9-eclipse-temurin-21"

// ContainerRunner executes Maven goals inside a Docker container.
// This replaces host-exec Maven invocations: the host does NOT need mvn or a JVM.
//
// Architecture:
//   - The container joins a shared Docker network (same as the Postgres container).
//   - The archetype clone directory is mounted at /work (read-write).
//   - The vendored Maven repo is mounted at /m2 (read-only) for offline resolution.
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
//   - networkName:   Name of the Docker network to join (from container.NewNetwork).
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

// Run executes mvn inside a freshly-created Maven container on the shared Docker network.
//
// The container:
//   - Mounts cloneRoot at /work (rw) — pom.xml, settings.xml, and archetype sources.
//   - Mounts repoCachePath at /m2 (ro) — vendored plugin + JDBC driver for offline resolution.
//   - Runs as the host UID:GID on Linux so files written into /work are not root-owned.
//   - Streams stdout/stderr to `out` and captures the full trace.
//   - Is removed when Run returns (AutoRemove / Terminate).
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
	paramStr := strings.Join(finalParams, " ")

	// Build the Maven command executed inside the container.
	// settings.xml is expected at /work/settings.xml (written by orchestrator into clone dir).
	cmd := []string{
		"mvn",
		"-f", "/work/pom.xml",
		"-B",
		"-s", "/work/settings.xml",
		"-Dmaven.repo.local=/m2",
		goal,
		"-Dparams=" + paramStr,
	}

	req := testcontainers.ContainerRequest{
		Image:    r.image,
		Networks: []string{r.networkName},
		// Entrypoint is empty — use the default image entrypoint (mvn).
		Cmd: cmd,
		HostConfigModifier: func(hc *dockercontainer.HostConfig) {
			hc.Binds = []string{
				cloneRoot + ":/work:rw",
				r.repoCachePath + ":/m2:ro",
			}
		},
	}

	// Set --user on Linux when not running as root, so files written into the
	// mounted clone dir are owned by the host user (not root) and os.RemoveAll
	// in cleanup succeeds without permission errors.
	if runtime.GOOS == "linux" && r.uid != 0 {
		userStr := fmt.Sprintf("%d:%d", r.uid, r.gid)
		req.ConfigModifier = func(c *dockercontainer.Config) {
			c.User = userStr
		}
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		// Check if context was cancelled before the container even started.
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
	defer func() { _ = c.Terminate(ctx) }()

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

	result := domain.StepResult{
		Name:       goal,
		Duration:   elapsed,
		DurationMs: elapsed.Milliseconds(),
	}

	// Check for context cancellation.
	if ctx.Err() != nil {
		result.Status = domain.StepStatusAborted
		result.Error = fmt.Sprintf("context cancelled: %v", ctx.Err())
		result.Trace = trace
		return result, nil
	}

	// Inspect the container's exit code.
	state, stateErr := c.State(ctx)
	exitCode := 0
	if stateErr != nil {
		// Cannot determine exit code — treat as failure.
		exitCode = 1
	} else {
		exitCode = state.ExitCode
	}

	if exitCode != 0 || strings.Contains(trace, "BUILD FAILURE") {
		result.Status = domain.StepStatusFailed
		if exitCode != 0 {
			result.Error = fmt.Sprintf("maven container exited with code %d", exitCode)
		} else {
			result.Error = "BUILD FAILURE detected in Maven output"
		}
		result.Trace = trace
		return result, nil
	}

	result.Status = domain.StepStatusPassed
	result.Trace = trace
	return result, nil
}

// Ensure ContainerRunner satisfies domain.MavenRunner at compile time.
var _ domain.MavenRunner = (*ContainerRunner)(nil)
