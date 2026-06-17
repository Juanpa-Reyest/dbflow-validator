// Package maven executes Maven goals in a cloned repository.
// Goal names and param format are kept as named constants to isolate
// reverse-engineered values from the plugin com.gs.ftt.coe-ds:relational-db-release-manager-plugin.
//
// IMPORTANT — param format discovery:
// The plugin's ParamParser regex is `--(\\w+)(?:=(.*?)(?=\\s+--|$))?`
// which matches SPACE-separated --KEY=VALUE tokens, not comma-separated.
// Example: `--TAG=abc123 --AUTHOR=validator-cli`
package maven

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/dbflow-validator/dbflow-validator/internal/domain"
)

// Reverse-engineered Maven goal names for the relational-db-release-manager-plugin.
const (
	GoalSync     = "dbflow:sync"
	GoalRollback = "dbflow:rollback"
)

// KV is a key-value pair used to build the -Dparams argument.
type KV struct {
	Key   string
	Value string
}

// FormatParams converts an ordered slice of KV pairs into a space-separated
// params string suitable for -Dparams="...".
// The plugin ParamParser regex splits on spaces between -- tokens.
// Example: [{--TAG abc123} {--AUTHOR validator-cli}] → "--TAG=abc123 --AUTHOR=validator-cli"
func FormatParams(pairs []KV) string {
	parts := make([]string, len(pairs))
	for i, kv := range pairs {
		parts[i] = kv.Key + "=" + kv.Value
	}
	return strings.Join(parts, " ")
}

// Runner executes Maven goals using exec.CommandContext.
//
// Deprecated: production code uses ContainerRunner. This type is retained
// because the unit tests in runner_test.go use fake mvn scripts to verify
// exit-code and BUILD FAILURE mapping logic without requiring Docker.
// Do not use Runner in new production code.
type Runner struct {
	// mvnBin is the path to the mvn binary. Defaults to "mvn" (resolved via PATH).
	mvnBin string
	// settingsPath is the optional path to a Maven settings.xml file passed via -s.
	// When non-empty, all mvn invocations include "-s <settingsPath>".
	settingsPath string
}

// NewRunner creates a Runner that invokes the given mvn binary.
// Pass "" to use "mvn" from PATH.
// Pass "" for settingsPath to use Maven's default settings.
func NewRunner(mvnBin, settingsPath string) *Runner {
	if mvnBin == "" {
		mvnBin = "mvn"
	}
	return &Runner{mvnBin: mvnBin, settingsPath: settingsPath}
}

// Run executes mvn -f <cloneRoot>/pom.xml -B <goal> -Dparams="<space-separated params>"
// in the cloned directory. It streams output to out AND captures it internally for
// the trace. A unique TAG is always prepended to the params unless one is already present.
//
// params is a slice of pre-formatted "--KEY=VALUE" strings.
//
// Exit-code mapping:
//   - exit 0 AND no "BUILD FAILURE" in stdout → StepStatusPassed
//   - exit 0 AND "BUILD FAILURE" in stdout    → StepStatusFailed
//   - exit != 0                               → StepStatusFailed
//   - ctx cancelled / killed                  → StepStatusAborted
func (r *Runner) Run(
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

	pomPath := cloneRoot + "/pom.xml"
	args := []string{
		"-f", pomPath,
		"-B",
	}
	// Inject custom settings.xml when specified (e.g., vendored offline repo).
	if r.settingsPath != "" {
		args = append(args, "-s", r.settingsPath)
	}
	args = append(args, goal, "-Dparams="+paramStr)

	cmd := exec.CommandContext(ctx, r.mvnBin, args...)
	cmd.Dir = cloneRoot

	// Capture full output for the trace while also streaming to out.
	var capture bytes.Buffer
	var mw io.Writer
	if out != nil {
		mw = io.MultiWriter(out, &capture)
	} else {
		mw = &capture
	}
	cmd.Stdout = mw
	cmd.Stderr = mw

	runErr := cmd.Run()
	elapsed := time.Since(start)
	trace := capture.String()

	result := domain.StepResult{
		Name:       goal,
		Duration:   elapsed,
		DurationMs: elapsed.Milliseconds(),
	}

	if ctx.Err() != nil {
		result.Status = domain.StepStatusAborted
		result.Error = fmt.Sprintf("context cancelled: %v", ctx.Err())
		result.Trace = trace
		return result, nil
	}

	if runErr != nil || strings.Contains(trace, "BUILD FAILURE") {
		result.Status = domain.StepStatusFailed
		if runErr != nil {
			result.Error = runErr.Error()
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

// ensureTagStr returns params unchanged if a --TAG= entry is already present,
// otherwise prepends --TAG=<uniqueTag> at the front.
func ensureTagStr(params []string, uniqueTag string) []string {
	for _, p := range params {
		if strings.HasPrefix(p, "--TAG=") {
			return params
		}
	}
	result := make([]string, 0, len(params)+1)
	result = append(result, "--TAG="+uniqueTag)
	result = append(result, params...)
	return result
}

// Ensure Runner satisfies domain.MavenRunner at compile time.
var _ domain.MavenRunner = (*Runner)(nil)
