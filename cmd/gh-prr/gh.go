package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func run(cmd []string) (string, error) {
	if len(cmd) == 0 {
		return "", errors.New("empty command")
	}

	execCmd := exec.Command(cmd[0], cmd[1:]...)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	if err := execCmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("command failed: %s", strings.Join(cmd, " "))
		}
		return "", errors.New(msg)
	}

	return stdout.String(), nil
}

func runWithStdin(cmd []string, stdin []byte) (string, error) {
	if len(cmd) == 0 {
		return "", errors.New("empty command")
	}

	execCmd := exec.Command(cmd[0], cmd[1:]...)
	execCmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	if err := execCmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("command failed: %s", strings.Join(cmd, " "))
		}
		return "", errors.New(msg)
	}

	return stdout.String(), nil
}

func ghJSON(cmd []string, v any) error {
	out, err := run(cmd)
	if err != nil {
		return err
	}

	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("command returned empty output: expected JSON response from %q", strings.Join(cmd, " "))
	}

	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	return nil
}

func getOwnerRepo() (string, string, error) {
	var resp struct {
		NameWithOwner string `json:"nameWithOwner"`
	}

	if err := ghJSON([]string{"gh", "repo", "view", "--json", "nameWithOwner"}, &resp); err != nil {
		return "", "", err
	}

	parts := strings.SplitN(resp.NameWithOwner, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected nameWithOwner: %q", resp.NameWithOwner)
	}

	return parts[0], parts[1], nil
}

func resolvePRNumber(provided *int) (int, error) {
	if provided != nil {
		return *provided, nil
	}

	var resp struct {
		Number json.Number `json:"number"`
	}

	if err := ghJSON([]string{"gh", "pr", "view", "--json", "number"}, &resp); err != nil {
		return 0, fmt.Errorf("failed to resolve PR number from current branch: %w", err)
	}

	if resp.Number == "" {
		return 0, fmt.Errorf("PR number not found for current branch. Specify a PR number explicitly or ensure you are on a branch with an open pull request")
	}

	prNumber, parseErr := strconv.Atoi(resp.Number.String())
	if parseErr != nil {
		return 0, fmt.Errorf("failed to parse PR number %q from gh JSON output: %w", resp.Number.String(), parseErr)
	}

	return prNumber, nil
}
