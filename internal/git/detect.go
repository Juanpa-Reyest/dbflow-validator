// Package git — detect.go provides heuristics to auto-detect the repository URL
// and base branch from the current working directory's git state.
//
// These helpers enable "zero-config" runs: when dbflow-validator is executed from
// within the archetype repository, it can infer --repo-url and --base-branch
// without requiring explicit flags or interactive prompts.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// DetectExecFunc is the injectable exec factory for detection commands.
// Pass nil to use the real exec.CommandContext.
type DetectExecFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// defaultDetectExec returns the real exec.CommandContext.
func defaultDetectExec(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// DetectRemoteURL returns the URL of the "origin" remote in the current working
// directory by running `git remote get-url origin`.
//
// Returns an error if:
//   - the cwd is not inside a git repository
//   - no "origin" remote is configured
//   - git is not installed
func DetectRemoteURL(ctx context.Context, execFn DetectExecFunc) (string, error) {
	if execFn == nil {
		execFn = defaultDetectExec
	}

	cmd := execFn(ctx, "git", "remote", "get-url", "origin")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		return "", fmt.Errorf("detect repo URL: %w (%s)", err, errMsg)
	}

	url := strings.TrimSpace(stdout.String())
	if url == "" {
		return "", fmt.Errorf("detect repo URL: origin remote returned empty URL")
	}
	return url, nil
}

// DetectBaseBranch attempts to determine the branch from which the current branch
// was created. It uses a series of heuristics in order of reliability:
//
//  1. Tracking upstream: `git rev-parse --abbrev-ref @{upstream}` — if the current
//     branch tracks a remote branch, extract its local name (e.g. "origin/prod" → "prod").
//  2. Merge-base heuristic: for each candidate base branch, compute the merge-base
//     with HEAD and select the candidate whose merge-base is closest (fewest commits
//     between merge-base and HEAD).
//
// candidateBranches is the list of branches to consider in the merge-base heuristic.
// If nil or empty, a sensible default is used.
//
// Returns an error only when no heuristic succeeds.
func DetectBaseBranch(ctx context.Context, execFn DetectExecFunc, candidateBranches []string) (string, error) {
	if execFn == nil {
		execFn = defaultDetectExec
	}

	// Strategy 1: tracking upstream.
	if branch, err := detectViaUpstream(ctx, execFn); err == nil && branch != "" {
		return branch, nil
	}

	// Strategy 2: merge-base with candidate branches.
	if len(candidateBranches) == 0 {
		candidateBranches = defaultCandidateBranches()
	}
	if branch, err := detectViaMergeBase(ctx, execFn, candidateBranches); err == nil && branch != "" {
		return branch, nil
	}

	return "", fmt.Errorf("detect base branch: could not determine base branch from upstream tracking or merge-base heuristic")
}

// defaultCandidateBranches returns the standard set of long-lived branches used
// in the merge-base heuristic. Order matters only for tie-breaking (first wins).
func defaultCandidateBranches() []string {
	return []string{
		"main",
		"master",
		"develop",
		"integration",
		"prod",
		"qa",
		"uat",
	}
}

// detectViaUpstream tries `git rev-parse --abbrev-ref @{upstream}` and extracts
// the branch name (strips the "origin/" prefix).
func detectViaUpstream(ctx context.Context, execFn DetectExecFunc) (string, error) {
	cmd := execFn(ctx, "git", "rev-parse", "--abbrev-ref", "@{upstream}")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}

	if err := cmd.Run(); err != nil {
		return "", err
	}

	upstream := strings.TrimSpace(stdout.String())
	if upstream == "" {
		return "", fmt.Errorf("empty upstream")
	}

	// Strip remote prefix: "origin/prod" → "prod"
	if idx := strings.Index(upstream, "/"); idx >= 0 {
		return upstream[idx+1:], nil
	}
	return upstream, nil
}

// detectViaMergeBase finds which candidate branch shares the most recent common
// ancestor with HEAD. The candidate with the fewest commits between merge-base
// and HEAD is selected (i.e., the one from which the current branch diverged
// most recently).
func detectViaMergeBase(ctx context.Context, execFn DetectExecFunc, candidates []string) (string, error) {
	type candidate struct {
		name     string
		distance int
	}

	var best *candidate

	for _, branch := range candidates {
		// Check if the remote-tracking branch exists.
		refName := "origin/" + branch
		checkCmd := execFn(ctx, "git", "rev-parse", "--verify", "--quiet", refName)
		checkCmd.Stdout = &bytes.Buffer{}
		checkCmd.Stderr = &bytes.Buffer{}
		if checkCmd.Run() != nil {
			// Also try local branch.
			checkCmd2 := execFn(ctx, "git", "rev-parse", "--verify", "--quiet", branch)
			checkCmd2.Stdout = &bytes.Buffer{}
			checkCmd2.Stderr = &bytes.Buffer{}
			if checkCmd2.Run() != nil {
				continue // branch doesn't exist locally or remotely
			}
			refName = branch
		}

		// Compute merge-base between HEAD and the candidate.
		mbCmd := execFn(ctx, "git", "merge-base", "HEAD", refName)
		var mbOut bytes.Buffer
		mbCmd.Stdout = &mbOut
		mbCmd.Stderr = &bytes.Buffer{}
		if mbCmd.Run() != nil {
			continue
		}
		mergeBase := strings.TrimSpace(mbOut.String())
		if mergeBase == "" {
			continue
		}

		// Count commits between merge-base and HEAD (distance = how far we diverged).
		countCmd := execFn(ctx, "git", "rev-list", "--count", mergeBase+"..HEAD")
		var countOut bytes.Buffer
		countCmd.Stdout = &countOut
		countCmd.Stderr = &bytes.Buffer{}
		if countCmd.Run() != nil {
			continue
		}

		distStr := strings.TrimSpace(countOut.String())
		dist := 0
		fmt.Sscanf(distStr, "%d", &dist)

		if best == nil || dist < best.distance {
			best = &candidate{name: branch, distance: dist}
		}
	}

	if best == nil {
		return "", fmt.Errorf("no candidate branch found via merge-base")
	}
	return best.name, nil
}
