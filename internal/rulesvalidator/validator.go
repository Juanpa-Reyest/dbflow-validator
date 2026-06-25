package rulesvalidator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// Option is a functional option for ContainerValidator.
type Option func(*ContainerValidator)

// WithValidatorOut sets the writer that receives the validator container's
// combined stdout+stderr. When nil or not provided, output is discarded (the
// default — consistent with the behaviour before this option was added).
// Wire this to execution.log in production to capture evidence of every run.
func WithValidatorOut(w io.Writer) Option {
	return func(v *ContainerValidator) {
		v.validatorOut = w
	}
}

// ContainerRunner is the interface satisfied by anything that can run the
// validator container and return the combined stdout+stderr output.
// It is a narrow seam so unit tests can inject a fake without Docker.
type ContainerRunner interface {
	RunValidator(ctx context.Context, req ValidatorContainerRequest) (string, error)
}

// ContainerValidator implements domain.PreSyncValidator by:
//  1. Locating the ruleset YAML and SQLInput directory under cloneRoot.
//  2. Building a ValidatorContainerRequest.
//  3. Running the JAR container and capturing the combined log.
//  4. Writing the captured log to validatorOut (when set) before gate decisions.
//  5. Extracting and parsing the JSON report.
//  6. Applying the gate decision.
type ContainerValidator struct {
	image   string
	jarPath string
	uid     int
	gid     int
	runner  ContainerRunner
	// validatorOut receives the container's combined stdout+stderr on every run,
	// regardless of outcome. Nil means discard (default, backward-compatible).
	validatorOut io.Writer
}

// Ensure ContainerValidator satisfies domain.PreSyncValidator at compile time.
var _ domain.PreSyncValidator = (*ContainerValidator)(nil)

// New creates a ContainerValidator.
//
// Parameters:
//   - image:   Docker image, e.g. "maven:3.9-eclipse-temurin-21".
//   - jarPath: Host-side absolute path to the extracted validator JAR.
//   - uid/gid: Host UID and GID for --user (pass 0 to skip on non-Linux).
//   - runner:  ContainerRunner implementation.  Pass nil to use the default
//              testcontainers-based runner.
//   - opts:    Optional functional options (e.g. WithValidatorOut).
func New(image, jarPath string, uid, gid int, runner ContainerRunner, opts ...Option) *ContainerValidator {
	if image == "" {
		image = "maven:3.9-eclipse-temurin-21"
	}
	if runner == nil {
		runner = &dockerRunner{}
	}
	v := &ContainerValidator{
		image:   image,
		jarPath: jarPath,
		uid:     uid,
		gid:     gid,
		runner:  runner,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// ValidatePreSync implements domain.PreSyncValidator.
//
// Flow:
//  1. Locate ruleset YAML and SQLInput dir inside cloneRoot (fails fast on missing ruleset).
//  2. Build container request (includes -output pointing inside the clone).
//  3. Run the JAR container — dockerRunner handles copy-in, execution, and copy-out.
//     The JSON report is written to req.ReportHostPath by RunValidator.
//  4. Read and parse the JSON report file from the host filesystem (ReportPath(cloneRoot)).
//  5. Apply the gate decision.
//
// Returns ("", nil) when globalSummary.status is "PASS" or "INFO" and no output
// was captured. Returns (containerLog, nil) when output was captured on a passing run.
// "INFO" means no applicable rules matched — informational, no actionable violations.
// Returns (containerLog, err) — always returning the captured log even on failure paths
// so the orchestrator can surface JAR evidence in StepResult.Trace.
// Returns a wrapped ErrNoReport if the JSON report file is absent or unparseable (fail-closed).
// Returns a hard error for FAIL, ERROR, or any other non-passing status.
// Returns ErrRulesetMissing (wrapped) if the ruleset YAML is absent from cloneRoot.
// Exit code of the container is intentionally ignored.
func (v *ContainerValidator) ValidatePreSync(ctx context.Context, cloneRoot string) (string, error) {
	// Step 1: Locate inputs (fails fast on missing ruleset).
	paths, err := Locate(cloneRoot)
	if err != nil {
		return "", fmt.Errorf("pre-sync-validate: %w", err)
	}

	// Step 2: Build container request.
	req := BuildContainerRequest(v.image, v.jarPath, v.uid, v.gid, cloneRoot, paths)

	// Step 3: Run the container.
	// RunValidator handles: create (Started:false) + JAR via Files + CopyDirToContainer clone →
	// /work + Start + log capture + Terminate + CopyFileFromContainer report → os.WriteFile(ReportHostPath).
	// The string return is the JAR's combined stdout+stderr (logger output).
	// Tee to validatorOut (the live sink, mirrors Maven's MavenOut tee) BEFORE
	// any gate decision so evidence is always recorded — even on failure paths.
	// ALSO return containerLog so the orchestrator can put it into StepResult.Trace.
	containerLog, runErr := v.runner.RunValidator(ctx, req)
	if v.validatorOut != nil && len(containerLog) > 0 {
		_, _ = io.WriteString(v.validatorOut, containerLog)
	}
	if runErr != nil {
		return containerLog, fmt.Errorf("pre-sync-validate: container execution: %w", runErr)
	}

	// Step 4: Read JSON report file written to the host by RunValidator.
	// ReportHostPath == ReportPath(cloneRoot); already written by RunValidator before we get here.
	reportPath := ReportPath(cloneRoot)
	rpt, err := ReadReportFile(reportPath)
	if err != nil {
		return containerLog, fmt.Errorf("pre-sync-validate: %w", err)
	}

	// Step 5: Gate decision.
	if err := Decide(rpt); err != nil {
		return containerLog, fmt.Errorf("pre-sync-validate: %w", err)
	}

	return containerLog, nil
}

// ---------------------------------------------------------------------------
// Default Docker-backed ContainerRunner (testcontainers-go)
// ---------------------------------------------------------------------------

// dockerRunner is the production ContainerRunner that uses testcontainers-go.
type dockerRunner struct{}

// RunValidator runs the validator container using the copy-in/copy-out lifecycle:
//
//  1. Create container (Started:false) with the JAR as a ContainerFile (copied at create).
//  2. CopyDirToContainer for each CopyDir in req (clone → /work).
//  3. Start the container.
//  4. Capture logs (stdout+stderr).
//  5. Terminate (deferred; safe on a never-started container too).
//  6. CopyFileFromContainer the JSON report from req.ReportContainerPath.
//  7. os.WriteFile the report bytes to req.ReportHostPath (failure-retention: report
//     lands in the clone BEFORE ReadReportFile; retained workspace contains it as evidence).
//
// Returns the combined container log string and nil on success.
// Exit code is intentionally ignored — gate decisions are made on JSON content.
func (d *dockerRunner) RunValidator(ctx context.Context, req ValidatorContainerRequest) (string, error) {
	tcReq := testcontainers.ContainerRequest{
		Image:    req.Image,
		Networks: req.Networks,
		Cmd:      req.Cmd,
		Env:      req.Env,
		// JAR is copied at container-create time via Files — no bind mount needed.
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      req.JarHostPath,
				ContainerFilePath: req.JarContainerPath,
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForExit(),
	}

	tcReq.ConfigModifier = func(c *dockercontainer.Config) {
		c.WorkingDir = containerWorkDir
		if runtime.GOOS == "linux" && req.User != "" {
			c.User = req.User
		}
	}

	// Create without starting so we can copy directories in before execution.
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: tcReq,
		Started:          false,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("context cancelled before container create: %w", ctx.Err())
		}
		return "", fmt.Errorf("create validator container: %w", err)
	}
	// Terminate is safe even on a created-but-not-started container (no-op).
	defer func() { _ = c.Terminate(ctx) }()

	// Copy each CopyDir into the container before starting it.
	for _, cd := range req.CopyDirs {
		if err := c.CopyDirToContainer(ctx, cd.HostPath, cd.ContainerParent, 0o755); err != nil {
			if ctx.Err() != nil {
				return "", fmt.Errorf("context cancelled during copy-in: %w", ctx.Err())
			}
			return "", fmt.Errorf("copy %q into validator container at %q: %w", cd.HostPath, cd.ContainerParent, err)
		}
	}

	// Start the container now that all inputs are present.
	if err := c.Start(ctx); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("context cancelled before container start: %w", ctx.Err())
		}
		return "", fmt.Errorf("start validator container: %w", err)
	}

	// Capture logs.
	var buf bytes.Buffer
	logs, logsErr := c.Logs(ctx)
	if logsErr != nil && ctx.Err() == nil {
		fmt.Fprintf(&buf, "[log-stream error: %v]\n", logsErr)
	}
	if logs != nil {
		_, _ = io.Copy(&buf, logs)
		logs.Close()
	}

	containerLog := buf.String()

	// Copy the JSON report out of the container to the host clone path.
	// This must happen BEFORE ReadReportFile is called (in ValidatePreSync step 4).
	// Writing to ReportHostPath ensures failure-retention: the workspace clone
	// contains the evidence report when it is retained on a failed run.
	if req.ReportContainerPath != "" && req.ReportHostPath != "" {
		rc, copyErr := c.CopyFileFromContainer(ctx, req.ReportContainerPath)
		if copyErr == nil {
			defer rc.Close()
			reportBytes, readErr := io.ReadAll(rc)
			if readErr == nil && len(reportBytes) > 0 {
				if mkdirErr := os.MkdirAll(filepath.Dir(req.ReportHostPath), 0o755); mkdirErr == nil {
					_ = os.WriteFile(req.ReportHostPath, reportBytes, 0o644)
				}
			}
		}
		// Copy-out failures are non-fatal here: if the report is absent,
		// ReadReportFile (step 4) will return ErrNoReport → fail-closed.
	}

	return containerLog, nil
}
