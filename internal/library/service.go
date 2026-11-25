package library

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/profile"
)

// Profiles persists individual records and supports concurrent calls.
type Profiles interface {
	List() ([]profile.App, error)
	Find(string) (profile.App, error)
	Put(profile.App) error
	Delete(string) error
}

// Bundles may receive concurrent calls for different application IDs.
type Bundles interface {
	Install(ctx context.Context, committed, app profile.App, icon icons.Artwork, save func() error) (string, error)
	UpdateSettings(ctx context.Context, committed, app profile.App, fallback func() icons.Artwork, save func() error) (string, error)
	Path(profile.App) (string, error)
	ReadInstalledArtwork(profile.App) func() icons.Artwork
	HasUsableArtwork(string) bool
	RefreshRuntime(context.Context, string) error
	Open(context.Context, string) error
	Remove(profile.App, func() error) error
}

// Launcher mirrors installed applications into the desktop's own app list.
// An unavailable app list can be retried independently of the saved library.
// Implementations may receive concurrent calls for different application IDs.
type Launcher interface {
	Register(profile.App, string) error
	Remove(string) error
}

type Options struct {
	Launcher    Launcher
	CustomIcons *icons.CustomStore
}

// Icon supplies the current preview. Custom marks a replacement chosen by the
// user. KeepInstalled reuses the current app's icon while editing its settings.
// CachedArtwork can supply a website icon from disk after the duplicate check.
type Icon struct {
	Artwork       icons.Artwork
	CachedArtwork IconSource
	Custom        bool
	KeepInstalled bool
}

// IconSource supplies local artwork for the current application settings.
type IconSource func(profile.App) icons.Artwork

type AlreadyExistsError struct{ App profile.App }

func (*AlreadyExistsError) Error() string { return "该网址已有同名应用" }

type Service struct {
	profiles    Profiles
	bundles     Bundles
	launcher    Launcher
	customIcons *icons.CustomStore

	apps         reservations
	addresses    reservations
	customMu     sync.Mutex
	pendingIcons map[string]int
	artwork      chan struct{}
	maintenance  chan struct{}
}

func New(profiles Profiles, bundles Bundles, options Options) *Service {
	return &Service{
		profiles: profiles, bundles: bundles, launcher: options.Launcher, customIcons: options.CustomIcons,
		artwork: make(chan struct{}, 1), maintenance: make(chan struct{}, 1),
	}
}

// Limit background packaging to one application at a time.
func (s *Service) reserveMaintenance(ctx context.Context, id string) (func(), error) {
	select {
	case s.maintenance <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release, err := s.apps.acquire(ctx, id)
	if err != nil {
		<-s.maintenance
		return nil, err
	}
	return func() {
		release()
		<-s.maintenance
	}, nil
}

func (s *Service) List() ([]profile.App, error) {
	apps, err := s.profiles.List()
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(apps, func(a, b profile.App) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return apps, nil
}

func (s *Service) InstalledArtwork(app profile.App) icons.Artwork {
	for {
		picture, busy := s.TryInstalledArtwork(app, nil)
		if busy == nil {
			return picture
		}
		<-busy
	}
}

// TryInstalledArtwork waits for a decoder or until stopped is closed. If a
// package operation holds the app, the returned channel announces its release.
func (s *Service) TryInstalledArtwork(app profile.App, stopped <-chan struct{}) (icons.Artwork, <-chan struct{}) {
	select {
	case s.artwork <- struct{}{}:
		defer func() { <-s.artwork }()
	case <-stopped:
		return icons.Artwork{}, nil
	}
	select {
	case <-stopped:
		return icons.Artwork{}, nil
	default:
	}
	release, busy := s.apps.claim(app.ID)
	if busy != nil {
		return icons.Artwork{}, busy
	}
	// Keep the paths stable while capturing the files. Decoding the captured
	// bytes can overlap settings changes for the same app.
	decode := s.bundles.ReadInstalledArtwork(app)
	release()
	return decode(), nil
}

func (s *Service) Save(ctx context.Context, draft profile.App, icon Icon) (profile.App, error) {
	return s.SaveWithSuggestedName(ctx, draft, icon, "")
}

func (s *Service) SaveWithSuggestedName(ctx context.Context, draft profile.App, icon Icon, suggestedName string) (profile.App, error) {
	if icon.Custom {
		draft.CustomIcon = ""
	}
	automaticName := draft.ID == "" && strings.TrimSpace(draft.Name) == ""
	if automaticName && profile.ValidName(suggestedName) {
		draft.Name = suggestedName
	}
	app, err := profile.Normalize(draft)
	if err != nil {
		return profile.App{}, err
	}
	newApp := app.ID == ""
	if newApp {
		app.ID = profile.NewID()
	}
	// Keep this app's installation and system entry stable across the save.
	release, err := s.apps.acquire(ctx, app.ID)
	if err != nil {
		return profile.App{}, err
	}
	defer release()
	app, path, changed, err := s.saveApplication(ctx, app, icon, automaticName, newApp)
	if err != nil {
		return profile.App{}, err
	}
	s.register(app, path, changed)
	return app, nil
}

func (s *Service) saveApplication(ctx context.Context, app profile.App, icon Icon, automaticName, newApp bool) (profile.App, string, bool, error) {
	now := time.Now().UTC()
	var existing profile.App
	if newApp {
		app.CreatedAt = now
		app.WindowRevision = 0
	} else {
		current, err := s.profiles.Find(app.ID)
		if err != nil {
			return profile.App{}, "", false, err
		}
		existing = current
		app.CreatedAt = existing.CreatedAt
		app.UpdatedAt = existing.UpdatedAt
		app.WindowRevision = existing.WindowRevision
		if app.Width != existing.Width || app.Height != existing.Height {
			app.WindowRevision++
		}
	}
	keepIcon := icon.KeepInstalled && !icon.Custom && existing.ID != "" && app.CustomIcon == existing.CustomIcon &&
		(app.CustomIcon != "" || (app.UsesAutomaticIcon(existing.URL) && app.Color == existing.Color))
	if keepIcon && app == existing {
		path, rebuilt, err := s.prepareInstalled(ctx, existing, func(profile.App) icons.Artwork { return icon.Artwork }, true)
		return existing, path, rebuilt, err
	}
	app.UpdatedAt = now
	renamed := !strings.EqualFold(app.Name, existing.Name)
	if renamed || app.URL != existing.URL {
		address, previous := addressKey(app.URL), addressKey(existing.URL)
		if renamed || address != previous {
			// A pending move affects duplicate checks at both addresses.
			release, err := s.addresses.acquirePair(ctx, address, previous)
			if err != nil {
				return profile.App{}, "", false, err
			}
			defer release()
			if err := s.checkDuplicates(app, address, automaticName); err != nil {
				return profile.App{}, "", false, err
			}
		}
	}
	return s.installApplication(ctx, existing, app, icon, keepIcon)
}

func (s *Service) checkDuplicates(app profile.App, address string, automaticName bool) error {
	apps, err := s.profiles.List()
	if err != nil {
		return err
	}
	var matchingAddress profile.App
	for _, other := range apps {
		if other.ID == app.ID {
			continue
		}
		sameName := strings.EqualFold(app.Name, other.Name)
		if !sameName && !automaticName {
			continue
		}
		if app.URL != other.URL && address != addressKey(other.URL) {
			continue
		}
		if sameName {
			return &AlreadyExistsError{App: other}
		}
		if matchingAddress.ID == "" {
			matchingAddress = other
		}
	}
	if matchingAddress.ID != "" {
		return &AlreadyExistsError{App: matchingAddress}
	}
	return nil
}

func (s *Service) installApplication(ctx context.Context, existing, app profile.App, icon Icon, keepIcon bool) (profile.App, string, bool, error) {
	picture := icon.Artwork
	if icon.Custom || app.CustomIcon != "" {
		var err error
		picture, err = s.prepareCustomIcon(&app, icon)
		if err != nil {
			return profile.App{}, "", false, err
		}
	}
	if !icon.Custom && !keepIcon {
		picture = s.appIcon(app, func(app profile.App) icons.Artwork {
			if icon.CachedArtwork != nil {
				if cached := icon.CachedArtwork(app); cached.Image != nil {
					return cached
				}
			}
			return icon.Artwork
		})
	}
	save := func() error { return s.profiles.Put(app) }
	var path string
	var err error
	if keepIcon {
		fallback := func() icons.Artwork {
			return s.appIcon(app, func(profile.App) icons.Artwork { return icon.Artwork })
		}
		path, err = s.bundles.UpdateSettings(ctx, existing, app, fallback, save)
	} else {
		path, err = s.bundles.Install(ctx, existing, app, picture, save)
	}
	if err != nil {
		s.finishCustomIcon(app.CustomIcon, app.CustomIcon)
		return profile.App{}, "", false, err
	}
	obsolete := existing.CustomIcon
	if obsolete == app.CustomIcon {
		obsolete = ""
	}
	s.finishCustomIcon(app.CustomIcon, obsolete)
	return app, path, true, nil
}

func addressKey(address string) string {
	u, err := profile.ParseAddress(address)
	if err != nil {
		return address
	}
	if u.Path == "" {
		u.Path = "/"
	}
	port := u.Port()
	host := strings.TrimSuffix(u.Host, ":"+port)
	if strings.HasPrefix(host, "[") {
		if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
			host = "[" + ip.String() + "]"
		}
	}
	port = strings.TrimLeft(port, "0")
	defaultPort := (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80")
	u.Host = host
	if port != "" && !defaultPort {
		u.Host += ":" + port
	}
	return u.String()
}

// RefreshIcon applies artwork to the latest settings for the same site.
func (s *Service) RefreshIcon(ctx context.Context, app profile.App, icon image.Image) (bool, error) {
	if icon == nil {
		return false, nil
	}
	release, err := s.reserveMaintenance(ctx, app.ID)
	if err != nil {
		return false, err
	}
	defer release()
	current, err := s.profiles.Find(app.ID)
	if errors.Is(err, profile.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current.UsesAutomaticIcon(app.URL) {
		return false, nil
	}
	path, err := s.bundles.Install(ctx, current, current, icons.Artwork{Image: icon, Kind: icons.WebsiteArtwork}, nil)
	if err != nil {
		return false, err
	}
	s.register(current, path, true)
	return true, nil
}

func (s *Service) Path(ctx context.Context, app profile.App, icon IconSource) (string, error) {
	release, err := s.apps.acquire(ctx, app.ID)
	if err != nil {
		return "", err
	}
	defer release()
	return s.ensure(ctx, app.ID, icon)
}

func (s *Service) Open(ctx context.Context, app profile.App, icon IconSource) error {
	release, err := s.apps.acquire(ctx, app.ID)
	if err != nil {
		return err
	}
	defer release()
	current, path, rebuilt, err := s.findInstalled(ctx, app.ID, icon)
	if err != nil {
		return err
	}
	// Open the verified path before attempting to repair the system entry.
	launchErr := s.bundles.Open(ctx, path)
	s.register(current, path, rebuilt)
	return launchErr
}

func (s *Service) ensure(ctx context.Context, id string, icon IconSource) (string, error) {
	current, path, rebuilt, err := s.findInstalled(ctx, id, icon)
	if err == nil {
		s.register(current, path, rebuilt)
	}
	return path, err
}

func (s *Service) findInstalled(ctx context.Context, id string, icon IconSource) (profile.App, string, bool, error) {
	current, err := s.profiles.Find(id)
	if err != nil {
		return profile.App{}, "", false, err
	}
	path, rebuilt, err := s.prepareInstalled(ctx, current, icon, false)
	return current, path, rebuilt, err
}

func (s *Service) prepareInstalled(ctx context.Context, current profile.App, icon IconSource, verifyArtwork bool) (string, bool, error) {
	path, err := s.bundles.Path(current)
	switch {
	case errors.Is(err, os.ErrNotExist), err == nil && verifyArtwork && !s.bundles.HasUsableArtwork(path):
		fallback := func() icons.Artwork {
			if picture := s.bundles.ReadInstalledArtwork(current)(); picture.Image != nil {
				return picture
			}
			return s.appIcon(current, icon)
		}
		path, err := s.bundles.UpdateSettings(ctx, current, current, fallback, nil)
		return path, true, err
	case err == nil:
		if refreshErr := s.bundles.RefreshRuntime(ctx, path); refreshErr != nil {
			log.Printf("update application runtime %s: %v", current.ID, refreshErr)
		}
	}
	return path, false, err
}

func (s *Service) Remove(id string) error {
	release, _ := s.apps.acquire(context.Background(), id)
	defer release()
	app, err := s.profiles.Find(id)
	if err != nil {
		return err
	}
	var triedLauncher bool
	err = s.bundles.Remove(app, func() error {
		if s.launcher != nil {
			triedLauncher = true
			removeErr := s.launcher.Remove(id)
			if removeErr != nil {
				return fmt.Errorf("remove system entry for %s: %w", id, removeErr)
			}
		}
		if err := s.profiles.Delete(id); err != nil {
			return err
		}
		s.finishCustomIcon("", app.CustomIcon)
		return nil
	})
	if err == nil || !triedLauncher {
		return err
	}
	// The bundle has rolled back. A failed remove may already have removed
	// some shortcuts, so restore the entry for the committed installation.
	path, restoreErr := s.bundles.Path(app)
	if errors.Is(restoreErr, os.ErrNotExist) {
		return err
	}
	if restoreErr == nil {
		restoreErr = s.register(app, path, true)
	}
	return errors.Join(err, restoreErr)
}

func (s *Service) register(app profile.App, path string, update bool) error {
	if s.launcher == nil {
		return nil
	}
	register := s.launcher.Register
	if current, ok := s.launcher.(interface {
		RegisterIfNeeded(profile.App, string) error
	}); !update && ok {
		register = current.RegisterIfNeeded
	}
	err := register(app, path)
	if err != nil {
		log.Printf("register launcher entry %s: %v", app.ID, err)
	}
	return err
}

func (s *Service) reclaimInterrupted(ctx context.Context) error {
	reclaimer, ok := s.bundles.(interface {
		ReclaimInterrupted(context.Context, func(string, bool, func() error) error) error
	})
	if !ok {
		return nil
	}
	profiles, canConfirm := s.profiles.(interface{ ConfirmAbsent(string) (bool, error) })
	return reclaimer.ReclaimInterrupted(ctx, func(id string, removal bool, reclaim func() error) error {
		release, busy := s.apps.claim(id)
		if busy != nil {
			return nil
		}
		defer release()
		if removal {
			if !canConfirm {
				return nil
			}
			absent, err := profiles.ConfirmAbsent(id)
			if err != nil || !absent {
				return err
			}
		}
		return reclaim()
	})
}

// RestoreLaunchers repairs the loaded library one app at a time using the
// latest saved profile, including edits made since startup.
func (s *Service) RestoreLaunchers(ctx context.Context, apps []profile.App, icon IconSource) error {
	failures := s.reclaimInterrupted(ctx)
	if s.launcher == nil {
		return failures
	}
	for _, app := range apps {
		if err := ctx.Err(); err != nil {
			return errors.Join(failures, err)
		}
		task, cancel := context.WithTimeout(ctx, 10*time.Second)
		release, err := s.reserveMaintenance(task, app.ID)
		if err == nil {
			_, err = s.ensure(task, app.ID, icon)
			release()
		}
		cancel()
		if err != nil && !errors.Is(err, profile.ErrNotFound) {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

func (s *Service) appIcon(app profile.App, source IconSource) icons.Artwork {
	if custom := s.customIcons.Get(app.CustomIcon); custom != nil {
		return icons.Artwork{Image: custom}
	}
	if source != nil {
		if icon := source(app); icon.Image != nil {
			return icon
		}
	}
	return icons.Artwork{Image: icons.Fallback(app.Name, app.Color), Kind: icons.PlaceholderArtwork}
}
