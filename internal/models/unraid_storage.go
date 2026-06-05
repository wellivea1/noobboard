package models

import "strings"

func NormalizeUnraidState(state string) string {
	return strings.ToLower(strings.TrimSpace(state))
}

func UnraidStorageReady(infra InfrastructureStatus) bool {
	if !infra.UnraidAPIReachable {
		return false
	}
	arrayState := NormalizeUnraidState(infra.UnraidArrayState)
	fsState := NormalizeUnraidState(infra.UnraidArrayFSState)
	// Work around Unraid API issue #1788: array.state can remain STARTED while
	// the WebGUI/storage layer is not usable. Prefer a future fixed array status
	// API when it becomes available.
	return arrayState == "started" && (fsState == "" || fsState == "started")
}

func UnraidStorageNeedsStart(infra InfrastructureStatus) bool {
	if !infra.UnraidAPIReachable {
		return false
	}
	arrayState := NormalizeUnraidState(infra.UnraidArrayState)
	fsState := NormalizeUnraidState(infra.UnraidArrayFSState)
	switch arrayState {
	case "stopped", "offline", "off", "down":
		return true
	}
	switch fsState {
	case "stopped", "stopping", "starting", "unmounted", "unmounting", "offline", "off", "down":
		return true
	default:
		return false
	}
}

func UnraidStorageDisplayState(infra InfrastructureStatus) string {
	fsState := NormalizeUnraidState(infra.UnraidArrayFSState)
	if fsState != "" && fsState != "started" {
		return "filesystem " + fsState
	}
	arrayState := NormalizeUnraidState(infra.UnraidArrayState)
	if arrayState != "" {
		return arrayState
	}
	return "not ready"
}
