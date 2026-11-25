package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Bistutu/Creator/internal/fileutil"
	"github.com/Bistutu/Creator/internal/profile"
)

type Appearance string

const (
	System Appearance = "system"
	Light  Appearance = "light"
	Dark   Appearance = "dark"
)

type StartPage string

const (
	CreatePage  StartPage = "create"
	LibraryPage StartPage = "library"
)

type Preferences struct {
	Appearance Appearance `json:"appearance"`
	StartPage  StartPage  `json:"startPage"`
	Width      int        `json:"windowWidth"`
	Height     int        `json:"windowHeight"`
}

func Default() Preferences {
	return Preferences{Appearance: System, StartPage: CreatePage, Width: profile.DefaultWidth, Height: profile.DefaultHeight}
}

func Load(path string) (Preferences, error) {
	defaults := Default()
	data, err := fileutil.ReadRegular(path, 8<<10)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	prefs := defaults
	if err := json.Unmarshal(data, &prefs); err != nil {
		return defaults, err
	}
	if err := prefs.validate(); err != nil {
		return defaults, err
	}
	return prefs, nil
}

func Save(path string, prefs Preferences) error {
	if err := prefs.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (p Preferences) validate() error {
	switch p.Appearance {
	case System, Light, Dark:
	default:
		return errors.New("外观设置无效")
	}
	if p.StartPage != CreatePage && p.StartPage != LibraryPage {
		return errors.New("启动页面设置无效")
	}
	return profile.ValidateWindowSize(p.Width, p.Height)
}
