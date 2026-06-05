package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wellivea1/noobboard/internal/config"
	"github.com/wellivea1/noobboard/internal/models"
)

func TestRuntimeSettingsPersistAndClone(t *testing.T) {
	path := cacheTestPath(t, "runtime-settings")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Defaults()
	settings := RuntimeSettings{
		Visibility: models.VisibilitySettings{
			HiddenAppIDs:         []string{"emby"},
			GeneralUserCanUseLLM: true,
		},
		Privacy: config.PrivacyConfig{
			BlacklistFolderPaths: []string{"/mnt/user/private"},
			RedactEmails:         true,
		},
		AppCatalog: config.AppCatalogConfig{
			IconOverrides:                map[string]string{"emby": "https://example.invalid/emby.png"},
			AgentRepairAllowed:           map[string]bool{"emby": true},
			GeneralUserRestartsEnabled:   true,
			GeneralUserAutoRepairEnabled: true,
			RestartAllowedGeneralUser:    map[string]bool{"emby": true},
		},
		LLM:           cfg.LLM,
		Notifications: cfg.Notifications,
	}
	if err := store.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}

	settings.Visibility.HiddenAppIDs[0] = "mutated"
	settings.AppCatalog.IconOverrides["emby"] = "mutated"
	settings.AppCatalog.AgentRepairAllowed["emby"] = false
	settings.AppCatalog.RestartAllowedGeneralUser["emby"] = false
	settings.AppCatalog.GeneralUserRestartsEnabled = false
	settings.AppCatalog.GeneralUserAutoRepairEnabled = false
	settings.LLM.Policies["admin_requested"] = models.LLMPolicy{}

	got, ok, err := store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("runtime settings were not saved")
	}
	if got.Visibility.HiddenAppIDs[0] != "emby" {
		t.Fatalf("saved visibility settings shared caller slice: %q", got.Visibility.HiddenAppIDs[0])
	}
	if got.LLM.Policies["admin_requested"].MaxContextBytes == 0 {
		t.Fatal("saved LLM policies shared caller map")
	}
	if got.AppCatalog.IconOverrides["emby"] != "https://example.invalid/emby.png" {
		t.Fatalf("saved app catalog shared caller map: %q", got.AppCatalog.IconOverrides["emby"])
	}
	if !got.AppCatalog.AgentRepairAllowed["emby"] {
		t.Fatalf("saved app repair settings shared caller map: %#v", got.AppCatalog.AgentRepairAllowed)
	}
	if !got.AppCatalog.GeneralUserRestartsEnabled || !got.AppCatalog.GeneralUserAutoRepairEnabled || !got.AppCatalog.RestartAllowedGeneralUser["emby"] {
		t.Fatalf("saved app user restart settings shared caller map: %#v", got.AppCatalog)
	}

	got.Visibility.HiddenAppIDs[0] = "changed"
	got.AppCatalog.IconOverrides["emby"] = "changed"
	got.AppCatalog.AgentRepairAllowed["emby"] = false
	got.AppCatalog.RestartAllowedGeneralUser["emby"] = false
	got.AppCatalog.GeneralUserRestartsEnabled = false
	got.AppCatalog.GeneralUserAutoRepairEnabled = false
	got.LLM.Policies["admin_requested"] = models.LLMPolicy{}
	gotAgain, _, err := store.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if gotAgain.Visibility.HiddenAppIDs[0] != "emby" {
		t.Fatalf("loaded visibility settings shared returned slice: %q", gotAgain.Visibility.HiddenAppIDs[0])
	}
	if gotAgain.LLM.Policies["admin_requested"].MaxContextBytes == 0 {
		t.Fatal("loaded LLM policies shared returned map")
	}
	if gotAgain.AppCatalog.IconOverrides["emby"] != "https://example.invalid/emby.png" {
		t.Fatalf("loaded app catalog shared returned map: %q", gotAgain.AppCatalog.IconOverrides["emby"])
	}
	if !gotAgain.AppCatalog.AgentRepairAllowed["emby"] {
		t.Fatalf("loaded app repair settings shared returned map: %#v", gotAgain.AppCatalog.AgentRepairAllowed)
	}
	if !gotAgain.AppCatalog.GeneralUserRestartsEnabled || !gotAgain.AppCatalog.GeneralUserAutoRepairEnabled || !gotAgain.AppCatalog.RestartAllowedGeneralUser["emby"] {
		t.Fatalf("loaded app user restart settings shared returned map: %#v", gotAgain.AppCatalog)
	}

	reopened, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok, err := reopened.RuntimeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("runtime settings did not persist")
	}
	if persisted.Privacy.BlacklistFolderPaths[0] != "/mnt/user/private" {
		t.Fatalf("persisted blacklist path = %q", persisted.Privacy.BlacklistFolderPaths[0])
	}
	if persisted.AppCatalog.IconOverrides["emby"] != "https://example.invalid/emby.png" {
		t.Fatalf("persisted icon override = %q", persisted.AppCatalog.IconOverrides["emby"])
	}
	if !persisted.AppCatalog.AgentRepairAllowed["emby"] {
		t.Fatalf("persisted app repair setting = %#v", persisted.AppCatalog.AgentRepairAllowed)
	}
	if !persisted.AppCatalog.GeneralUserRestartsEnabled || !persisted.AppCatalog.GeneralUserAutoRepairEnabled || !persisted.AppCatalog.RestartAllowedGeneralUser["emby"] {
		t.Fatalf("persisted app user restart setting = %#v", persisted.AppCatalog)
	}
}

func cacheTestPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join("..", "..", ".cache", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name+"-"+time.Now().UTC().Format("20060102150405.000000000")+".json")
}
