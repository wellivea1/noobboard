package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wellivea1/server-status/internal/config"
	"github.com/wellivea1/server-status/internal/models"
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
			IconOverrides: map[string]string{"emby": "https://example.invalid/emby.png"},
		},
		LLM:           cfg.LLM,
		Notifications: cfg.Notifications,
	}
	if err := store.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}

	settings.Visibility.HiddenAppIDs[0] = "mutated"
	settings.AppCatalog.IconOverrides["emby"] = "mutated"
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

	got.Visibility.HiddenAppIDs[0] = "changed"
	got.AppCatalog.IconOverrides["emby"] = "changed"
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
}

func cacheTestPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join("..", "..", ".cache", "tests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name+"-"+time.Now().UTC().Format("20060102150405.000000000")+".json")
}
