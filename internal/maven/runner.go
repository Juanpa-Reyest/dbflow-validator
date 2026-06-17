// Package maven executes Maven goals in a cloned repository.
// Goal names and ROLLBACK_MODE are kept as named constants to isolate
// reverse-engineered values from the plugin com.gs.ftt.coe-ds:relational-db-release-manager-plugin.
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
// Validate against both archetypes at integration time.
const (
	GoalSync     = "dbflow:sync"
	GoalRollback = "dbflow:rollback"
)

// RollbackModeStandard is the ROLLBACK_MODE value observed in GHA workflows.
const RollbackModeStandard = "Standard"

// KV is a key-value pair used to build the -Dparams argument.
type KV struct {
	Key   string
	Value string
}

// FormatParams converts an ordered slice of KV pairs into a comma-separated
// params string suitable for -Dparams="...".
// Example: [{--TAG 210} {--ROLLBACK_MODE Standard}] → "--TAG=210,--ROLLBACK_MODE=Standard"
func FormatParams(pairs []KV) string {
	parts := make([]string, len(pairs))
	for i, kv := range pairs {
		parts[i] = kv.Key + "=" + kv.Value
	}
	return strings.Join(parts, ",")
}

// Runner executes Maven goals using exec.CommandContext.
type Runner struct {
	// mvnBin is the path to the mvn binary. Defaults to "mvn" (resolved via PATH).
	// Set to a fake binary path in tests.
	mvnBin string
}

// NewRunner creates a Runner that invokes the given mvn binary.
// Pass "" to use "mvn" from PATH.
func NewRunner(mvnBin string) *Runner {
	if mvnBin == "" {
		mvnBin = "mvn"
	}
	return &Runner{mvnBin: mvnBin}
}

// Run executes mvn -f <cloneRoot>/pom.xml -B <goal> -Dparams="<formatted pairs>"
// in the cloned directory. It streams output to out AND captures it internally for
// the trace. The TAG param is always injected as unique (RFC3339Nano timestamp)
// unless already present in pairs.
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
	params []KV,
	out io.Writer,
) (domain.StepResult, error) {
	start := time.Now()

	// Inject unique TAG if not provided (sync goal uses this).
	uniqueTag := time.Now().Format(time.RFC3339Nano)
	finalParams := ensureTag(params, uniqueTag)

	paramStr := FormatParams(finalParams)

	pomPath := cloneRoot + "/pom.xml"
	args := []string{
		"-f", pomPath,
		"-B",
		goal,
		"-Dparams=" + paramStr,
	}

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

// ensureTag returns params unchanged if a --TAG key is already present,
// otherwise prepends --TAG=<uniqueTag> at the front.
func ensureTag(params []KV, uniqueTag string) []KV {
	for _, kv := range params {
		if kv.Key == "--TAG" {
			return params
		}
	}
	tagged := make([]KV, 0, len(params)+1)
	tagged = append(tagged, KV{Key: "--TAG", Value: uniqueTag})
	tagged = append(tagged, params...)
	return tagged
}
