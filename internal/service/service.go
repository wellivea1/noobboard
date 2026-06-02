package service

type Options struct {
	Name        string
	DisplayName string
	Description string
	Version     string
}

func OptionsFromVersion(version string) Options {
	return Options{
		Name:        "NoobBoard",
		DisplayName: "NoobBoard",
		Description: "Local NoobBoard diagnostics dashboard.",
		Version:     version,
	}
}
