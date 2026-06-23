package rulesvalidator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

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
//  4. Extracting and parsing the JSON report.
//  5. Applying the gate decision.
type ContainerValidator struct {
	image      string
	jarPath    string
	uid        int
	gid        int
	runner     ContainerRunner
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
func New(image, jarPath string, uid, gid int, runner ContainerRunner) *ContainerValidator {
	if image == "" {
		image = "maven:3.9-eclipse-temurin-21"
	}
	if runner == nil {
		runner = &dockerRunner{}
	}
	return &ContainerValidator{
		image:   image,
		jarPath: jarPath,
		uid:     uid,
		gid:     gid,
		runner:  runner,
	}
}

// ValidatePreSync implements domain.PreSyncValidator.
//
// It runs the validator JAR in a container against the cloneRoot, parses the
// JSON report embedded in the combined output, and applies the gate decision.
//
// Returns nil only when globalSummary.status == "PASS".
// Returns a hard error for FAIL, ERROR, unknown status, or missing/unparseable JSON.
// Returns ErrRulesetMissing (wrapped) if the ruleset YAML is absent from cloneRoot.
func (v *ContainerValidator) ValidatePreSync(ctx context.Context, cloneRoot string) error {
	// Step 1: Locate inputs (fails fast on missing ruleset).
	paths, err := Locate(cloneRoot)
	if err != nil {
		return fmt.Errorf("pre-sync-validate: %w", err)
	}

	// Step 2: Build container request.
	req := BuildContainerRequest(v.image, v.jarPath, v.uid, v.gid, cloneRoot, paths)

	// Step 3: Run the container.
	output, err := v.runner.RunValidator(ctx, req)
	if err != nil {
		return fmt.Errorf("pre-sync-validate: container execution: %w", err)
	}

	// Step 4: Extract JSON report.
	rpt, err := ExtractReport(output)
	if err != nil {
		return fmt.Errorf("pre-sync-validate: %w", err)
	}

	// Step 5: Gate decision.
	if err := Decide(rpt); err != nil {
		return fmt.Errorf("pre-sync-validate: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Default Docker-backed ContainerRunner (testcontainers-go)
// ---------------------------------------------------------------------------

// dockerRunner is the production ContainerRunner that uses testcontainers-go.
type dockerRunner struct{}

// RunValidator starts the validator container, captures combined stdout+stderr,
// waits for exit, and returns the full output string.
// Exit code is intentionally ignored — gate decisions are made on JSON content.
func (d *dockerRunner) RunValidator(ctx context.Context, req ValidatorContainerRequest) (string, error) {
	tcReq := testcontainers.ContainerRequest{
		Image:    req.Image,
		Networks: req.Networks,
		Cmd:      req.Cmd,
		Env:      req.Env,
		HostConfigModifier: func(hc *dockercontainer.HostConfig) {
			hc.Binds = req.Binds
		},
		WaitingFor: wait.ForExit(),
	}

	tcReq.ConfigModifier = func(c *dockercontainer.Config) {
		c.WorkingDir = containerWorkDir
		if runtime.GOOS == "linux" && req.User != "" {
			c.User = req.User
		}
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: tcReq,
		Started:          true,
	})
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("context cancelled before container start: %w", ctx.Err())
		}
		return "", fmt.Errorf("start validator container: %w", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	var buf bytes.Buffer
	logs, logsErr := c.Logs(ctx)
	if logsErr != nil && ctx.Err() == nil {
		fmt.Fprintf(&buf, "[log-stream error: %v]\n", logsErr)
	}
	if logs != nil {
		_, _ = io.Copy(&buf, logs)
		logs.Close()
	}

	return buf.String(), nil
}
