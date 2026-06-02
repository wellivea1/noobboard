package service

type Options struct {
	Name        string
	DisplayName string
	Description string
	Version     string
}

func OptionsFromVersion(version string) Options {
	return Options{
		Name:        "ServerStatus",
		DisplayName: "Server Status",
		Description: "Local server-status and diagnostics dashboard.",
		Version:     version,
	}
}
