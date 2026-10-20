//go:build darwin

package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func pickFile(title, filterName, filterPattern, startDir string) (string, error) {
	script := "POSIX path of (choose file with prompt " + appleScriptQuote(title)
	if ext := strings.TrimPrefix(filterPattern, "*."); ext != "" && ext != filterPattern && ext != "*" {
		script += " of type {" + appleScriptQuote(ext) + "}"
	}
	script += defaultLocationClause(startDir) + ")"
	return runAppleScript(script)
}

func pickFolder(title, startDir string) (string, error) {
	path, err := runAppleScript("POSIX path of (choose folder with prompt " + appleScriptQuote(title) + defaultLocationClause(startDir) + ")")
	if err != nil || len(path) <= 1 {
		return path, err
	}
	return strings.TrimRight(path, "/"), nil
}

func defaultLocationClause(startDir string) string {
	if startDir == "" {
		return ""
	}
	return " default location POSIX file " + appleScriptQuote(startDir)
}

func runAppleScript(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if strings.Contains(stderr, "(-128)") || strings.Contains(stderr, "User canceled") {
			return "", nil
		}
		if stderr != "" {
			return "", fmt.Errorf("file dialog failed: %s", stderr)
		}
	}
	return "", fmt.Errorf("file dialog failed: %w", err)
}

func appleScriptQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
