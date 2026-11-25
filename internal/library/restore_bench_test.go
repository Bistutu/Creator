package library_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

type readyBundles struct{ library.Bundles }

func (readyBundles) Path(profile.App) (string, error)             { return "installed-app", nil }
func (readyBundles) RefreshRuntime(context.Context, string) error { return nil }

type readyLauncher struct{}

func (readyLauncher) Register(profile.App, string) error { return nil }
func (readyLauncher) Remove(string) error                { return nil }

// Measure profile work during a startup repair independently of native shell
// registration, which cannot run on every build host.
func BenchmarkRestoreStoredLaunchers(b *testing.B) {
	for _, count := range []int{120, 1200} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			apps := make([]profile.App, count)
			for i := range apps {
				apps[i] = profile.App{
					ID: profile.NewID(), Name: fmt.Sprintf("Workspace %d", i),
					URL:   fmt.Sprintf("https://site-%d.example.org/desk", i),
					Width: 880, Height: 640, Category: "效率", Color: profile.DefaultColor,
				}
			}
			data, err := json.Marshal(apps)
			if err != nil {
				b.Fatal(err)
			}
			path := filepath.Join(b.TempDir(), "apps.json")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				b.Fatal(err)
			}
			svc := library.New(profile.NewStore(path), readyBundles{}, library.Options{Launcher: readyLauncher{}})
			loaded, err := svc.List()
			if err != nil || len(loaded) != count {
				b.Fatalf("load %d profiles: %d, %v", count, len(loaded), err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := svc.RestoreLaunchers(b.Context(), loaded, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
