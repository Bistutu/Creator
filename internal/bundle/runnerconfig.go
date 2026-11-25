package bundle

import (
	"encoding/json"
	"os"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
	"github.com/Bistutu/Creator/internal/runnerconfig"
)

const (
	ConfigName    = runnerconfig.Filename
	maxConfigSize = runnerconfig.MaxSize
)

// RunnerConfig is the data required to open a packaged website.
type RunnerConfig = runnerconfig.Config

type iconMetadata struct {
	IconKind   icons.ArtworkKind `json:"iconKind,omitempty"`
	IconDigest string            `json:"iconDigest,omitempty"`
}

type installedConfig struct {
	RunnerConfig
	iconMetadata
}

func writeConfig(path string, app profile.App, icon iconMetadata) error {
	data, err := json.Marshal(installedConfig{RunnerConfig: configForApp(app), iconMetadata: icon})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func checkConfig(path string, app profile.App) error {
	config, err := readConfig(path)
	if err != nil {
		return err
	}
	if config.RunnerConfig != configForApp(app) {
		return os.ErrNotExist
	}
	return nil
}

func readConfig(path string) (installedConfig, error) {
	data, err := readBundleResource(path, maxConfigSize)
	if err != nil {
		return installedConfig{}, err
	}
	return decodeConfig(data)
}

func decodeConfig(data []byte) (installedConfig, error) {
	var config installedConfig
	if json.Unmarshal(data, &config) != nil {
		return installedConfig{}, os.ErrNotExist
	}
	return config, nil
}

func configForApp(app profile.App) RunnerConfig {
	return RunnerConfig{
		ID: app.ID, Name: app.Name, URL: app.URL, Width: app.Width, Height: app.Height,
		WindowRevision: app.WindowRevision,
	}
}
