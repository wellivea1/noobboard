//go:build windows

package service

import (
	"fmt"
	"os"
	"os/exec"
)

func Install(options Options) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binPath := fmt.Sprintf(`"%s" serve`, exe)
	if err := exec.Command("sc.exe", "create", options.Name, "binPath=", binPath, "start=", "auto", "DisplayName=", options.DisplayName).Run(); err != nil {
		return err
	}
	_ = exec.Command("sc.exe", "description", options.Name, options.Description).Run()
	return exec.Command("sc.exe", "failure", options.Name, "reset=", "86400", "actions=", "restart/60000/restart/60000/none/60000").Run()
}

func Uninstall(options Options) error {
	_ = Stop(options)
	return exec.Command("sc.exe", "delete", options.Name).Run()
}

func Start(options Options) error {
	return exec.Command("sc.exe", "start", options.Name).Run()
}

func Stop(options Options) error {
	return exec.Command("sc.exe", "stop", options.Name).Run()
}
