//go:build !windows && !darwin

package tui

import "errors"

func pickFile(title, filterName, filterPattern, startDir string) (string, error) {
	return "", errors.New("native file dialogs are only available on windows and macos")
}

func pickFolder(title, startDir string) (string, error) {
	return "", errors.New("native file dialogs are only available on windows and macos")
}
