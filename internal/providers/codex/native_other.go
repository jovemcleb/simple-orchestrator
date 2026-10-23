//go:build !windows

package codex

import "errors"

func DefaultNativeLauncher(dir string, argv []string) (<-chan struct{}, func(), error) {
	return nil, nil, errors.New("--watch-native is only supported on Windows")
}
