//go:build !windows

package service

import "fmt"

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
