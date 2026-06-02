//go:build !windows

package service

import (
	"context"
	"fmt"
)

// RunService is a no-op off Windows: there is no Service Control Manager, so the caller
// always runs in the foreground.
func RunService(_ string, _ func(ctx context.Context) error) (bool, error) {
	return false, nil
}

func Install(options Options) error {
	return fmt.Errorf("%s service installation is only implemented on Windows in this build", options.Name)
}

func Uninstall(options Options) error {
	return fmt.Errorf("%s service uninstallation is only implemented on Windows in this build", options.Name)
}

func Start(options Options) error {
	return fmt.Errorf("%s service start is only implemented on Windows in this build", options.Name)
}

func Stop(options Options) error {
	return fmt.Errorf("%s service stop is only implemented on Windows in this build", options.Name)
}
