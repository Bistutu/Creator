package library

import (
	"errors"
	"log"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

// Keep the source image available until the profile that refers to it commits.
func (s *Service) prepareCustomIcon(app *profile.App, icon Icon) (icons.Artwork, error) {
	if s.customIcons == nil {
		if icon.Custom {
			return icons.Artwork{}, errors.New("无法保存自定义图标")
		}
		return icon.Artwork, nil
	}
	s.customMu.Lock()
	defer s.customMu.Unlock()
	picture := icon.Artwork
	if icon.Custom {
		var err error
		app.CustomIcon, picture, err = s.customIcons.Put(picture.Image)
		if err != nil {
			return icons.Artwork{}, err
		}
	}
	if s.pendingIcons == nil {
		s.pendingIcons = make(map[string]int)
	}
	s.pendingIcons[app.CustomIcon]++
	return picture, nil
}

func (s *Service) finishCustomIcon(key, obsolete string) {
	if s.customIcons == nil || (key == "" && obsolete == "") {
		return
	}
	s.customMu.Lock()
	defer s.customMu.Unlock()
	if key != "" {
		if s.pendingIcons[key] > 1 {
			s.pendingIcons[key]--
		} else {
			delete(s.pendingIcons, key)
		}
	}
	if obsolete == "" || s.pendingIcons[obsolete] != 0 {
		return
	}
	apps, err := s.profiles.List()
	if err != nil {
		log.Printf("check custom icon references: %v", err)
		return
	}
	for _, app := range apps {
		if app.CustomIcon == obsolete {
			return
		}
	}
	if err := s.customIcons.Remove(obsolete); err != nil {
		log.Printf("remove unused custom icon: %v", err)
	}
}
