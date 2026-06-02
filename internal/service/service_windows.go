//go:build windows

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/windows/svc"
)

// RunService runs fn under the Windows Service Control Manager when the process was launched
// as a service. It reports StartPending/Running/Stopped so SCM's start request succeeds, and
// cancels fn's context on Stop/Shutdown. When the process is running interactively (from a
// console), it returns handled=false so the caller runs fn in the foreground instead.
func RunService(name string, fn func(ctx context.Context) error) (handled bool, err error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("determine windows service mode: %w", err)
	}
	if !isService {
		return false, nil
	}
	return true, svc.Run(name, &handler{fn: fn})
}

type handler struct {
	fn func(ctx context.Context) error
}

func (h *handler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.fn(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				status <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		case err := <-done:
			// fn exited on its own (e.g. a listener failed). Report stopped, with a
			// non-zero exit code on error so SCM marks the service failed.
			status <- svc.Status{State: svc.StopPending}
			if err != nil {
				status <- svc.Status{State: svc.Stopped}
				return false, 1
			}
			status <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
}

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
