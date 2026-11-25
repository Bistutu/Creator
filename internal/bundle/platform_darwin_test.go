package bundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bistutu/Creator/internal/icons"
	"github.com/Bistutu/Creator/internal/library"
	"github.com/Bistutu/Creator/internal/profile"
)

func TestMacCreatesSignedStandaloneApplicationOffline(t *testing.T) {
	bundles, service := releaseFixture(t)
	t.Setenv("PATH", t.TempDir()) // The manager must work without Go, Xcode or third-party tools in PATH.
	started := time.Now()
	icon := icons.Fallback("A", "#c23365")
	app, err := service.Save(t.Context(), profile.App{Name: `A & B <Mac>`, URL: "https://example.com/app", Width: 960, Height: 640},
		library.Icon{Artwork: icons.Artwork{Image: icon}, Custom: true})
	if err != nil {
		t.Fatal(err)
	}
	creation := time.Since(started)
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--deep", path).CombinedOutput(); err != nil {
		t.Fatalf("macOS signature: %v (%s)", err, output)
	}
	contents := filepath.Join(path, "Contents")
	name, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print CFBundleDisplayName", filepath.Join(contents, "Info.plist")).Output()
	if err != nil || string(bytes.TrimSpace(name)) != app.Name {
		t.Fatalf("Finder name: %q, %v", name, err)
	}
	checkMediaUsageDescriptions(t, filepath.Join(contents, "Info.plist"))
	data, err := os.ReadFile(filepath.Join(contents, "Resources", ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	var config RunnerConfig
	if err := json.Unmarshal(data, &config); err != nil || config.URL != app.URL || config.Width != 960 || config.ID != app.ID {
		t.Fatalf("standalone config = %+v, %v", config, err)
	}
	art, err := os.ReadFile(filepath.Join(contents, "Resources", "AppIcon.icns"))
	if err != nil {
		t.Fatal(err)
	}
	if len(art) <= 12 || string(art[:4]) != "icns" || binary.BigEndian.Uint32(art[4:8]) != uint32(len(art)) {
		t.Fatalf("invalid Finder icon")
	}
	for _, entry := range []string{"icp4", "icp5", "icp6", "ic07", "ic08"} {
		if !bytes.Contains(art, []byte(entry)) {
			t.Errorf("missing %s from Finder icon", entry)
		}
	}
	if !profile.ValidIconKey(app.CustomIcon) {
		t.Fatal("the profile does not refer to its custom icon")
	}
	for offset := 8; offset < len(art); {
		if len(art)-offset < 8 {
			t.Fatal("truncated ICNS frame header")
		}
		length := int(binary.BigEndian.Uint32(art[offset+4 : offset+8]))
		if length < 8 || length > len(art)-offset {
			t.Fatal("invalid ICNS frame")
		}
		if string(art[offset:offset+4]) == "ic07" {
			frame, err := png.Decode(bytes.NewReader(art[offset+8 : offset+length]))
			if err != nil || frame.At(14, 64) != icon.At(14, 64) {
				t.Fatalf("Finder received the wrong custom icon: %v", err)
			}
		}
		offset += length
	}
	size := int64(0)
	if err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		info, err := entry.Info()
		if err == nil {
			size += info.Size()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("offline creation %v, .app logical size %d bytes (%d KiB)", creation, size, (size+1023)/1024)
	if size > 350<<10 {
		t.Errorf("standalone .app grew beyond 350 KiB: %d bytes", size)
	}
}

func checkMediaUsageDescriptions(t *testing.T, plist string) {
	t.Helper()
	for _, kind := range []struct{ key, device string }{
		{"NSCameraUsageDescription", "摄像头"}, {"NSMicrophoneUsageDescription", "麦克风"},
	} {
		out, err := exec.CommandContext(t.Context(), "/usr/bin/plutil", "-extract", kind.key, "raw", "-expect", "string", "-o", "-", plist).CombinedOutput()
		if err != nil || !strings.Contains(string(out), kind.device) {
			t.Errorf("%s must explain the website's use of %s: %s (%v)", kind.key, kind.device, out, err)
		}
	}
}

func TestMacRecoversSignedAppWithItsOriginalIcon(t *testing.T) {
	bundles, service := releaseFixture(t)
	t.Setenv("PATH", t.TempDir())
	url := "https://example.com/saved"
	var requests chan string
	if os.Getenv("WEBCRATE_GUI_SMOKE") == "1" {
		requests = make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case requests <- r.URL.Path:
			default:
			}
			_, _ = w.Write([]byte("<title>Recovered application</title>"))
		}))
		t.Cleanup(server.Close)
		url = server.URL + "/saved"
	}
	icon := icons.Fallback("S", "#c23365")
	saved, err := service.Save(t.Context(), profile.App{Name: "Saved app", URL: url},
		library.Icon{Artwork: icons.Artwork{Image: icon}, Custom: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(saved)
	if err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(path, "Contents", "MacOS", darwinRunner)
	before := fileInfo(t, runner)
	iconPath := filepath.Join(path, "Contents", "Resources", "AppIcon.icns")
	originalIcon, err := os.ReadFile(iconPath)
	if err != nil {
		t.Fatal(err)
	}

	draft := saved
	draft.Name, draft.URL, draft.CustomIcon = "Pending app", url+"/proposed", ""
	staging := NewLibrary(t.TempDir(), bundles.backend)
	proposed, err := staging.Install(t.Context(), profile.App{}, draft, icons.Artwork{Image: icons.Brand()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir, backup := bundles.paths(saved.ID)
	if err := os.Rename(dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(proposed), dir); err != nil {
		t.Fatal(err)
	}
	loaded := service.InstalledArtwork(saved).Image
	if loaded == nil || loaded.At(14, 64) != icon.At(14, 64) {
		t.Fatal("the library artwork did not read the last committed icon from the backup")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(proposed))); err != nil {
		t.Fatalf("reading the icon modified the pending bundle: %v", err)
	}

	if requests != nil {
		// Launch through the manager and Launch Services, as a library click does.
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		realRunner := filepath.Join(realDir, filepath.Base(path), "Contents", "MacOS", darwinRunner)
		t.Cleanup(func() {
			pattern := "^(" + regexp.QuoteMeta(runner) + "|" + regexp.QuoteMeta(realRunner) + ")($| )"
			output, _ := exec.Command("/usr/bin/pgrep", "-f", pattern).Output()
			for _, id := range strings.Fields(string(output)) {
				pid, err := strconv.Atoi(id)
				if err == nil {
					if process, err := os.FindProcess(pid); err == nil {
						_ = process.Kill()
					}
				}
			}
		})
		if err := service.Open(t.Context(), saved, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case route := <-requests:
			if route != "/saved" {
				t.Fatalf("recovered app loaded %q", route)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("recovered app did not load the committed website")
		}
	}

	recovered, err := service.Path(t.Context(), saved, nil)
	if err != nil || recovered != path {
		t.Fatalf("recovered app = %q, %v", recovered, err)
	}
	if !os.SameFile(before, fileInfo(t, runner)) {
		t.Fatal("recovery rebuilt the saved app")
	}
	restoredIcon, err := os.ReadFile(iconPath)
	if err != nil || !bytes.Equal(restoredIcon, originalIcon) {
		t.Fatalf("recovery changed the saved app's icon: %v", err)
	}
	if output, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--deep", recovered).CombinedOutput(); err != nil {
		t.Fatalf("recovered app signature: %v (%s)", err, output)
	}
}

func TestMacRuntimeUpdateKeepsSignedAppAndItsResources(t *testing.T) {
	bundles, service := releaseFixture(t)
	backend := bundles.backend.(darwinBackend)
	legacy := backend
	legacy.runtimeDigest = fmt.Appendf(nil, "%x", sha256.Sum256(legacy.runtime))
	bundles.backend = legacy
	saved, err := service.Save(t.Context(), profile.App{Name: "Saved app", URL: "https://example.com/saved"},
		library.Icon{Artwork: icons.Artwork{Image: icons.Fallback("S", "#c23365")}, Custom: true})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(saved)
	if err != nil {
		t.Fatal(err)
	}
	read := func(rel string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(path, "Contents", rel))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	marker := filepath.Join("Resources", runtimeMarker)
	icon := filepath.Join("Resources", "AppIcon.icns")
	config := filepath.Join("Resources", ConfigName)
	contents := filepath.Join(path, "Contents")
	plist := filepath.Join(contents, "Info.plist")
	editPlist := func(args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(t.Context(), "/usr/bin/plutil", append(args, plist)...).CombinedOutput(); err != nil {
			t.Fatalf("prepare legacy Info.plist: %v (%s)", err, out)
		}
	}
	editPlist("-remove", "NSCameraUsageDescription")
	editPlist("-remove", "NSMicrophoneUsageDescription")
	editPlist("-insert", "CreatorFixtureSetting", "-bool", "YES")
	editPlist("-convert", "binary1")
	if err := signBundle(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	oldModification := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, oldModification, oldModification); err != nil {
		t.Fatal(err)
	}
	readMetadata := func() map[string]any {
		t.Helper()
		out, err := exec.CommandContext(t.Context(), "/usr/bin/plutil", "-convert", "json", "-o", "-", plist).CombinedOutput()
		if err != nil {
			t.Fatalf("read installed Info.plist: %v (%s)", err, out)
		}
		var fields map[string]any
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatal(err)
		}
		return fields
	}
	appDirectory, originalContents := fileInfo(t, path), fileInfo(t, contents)
	previousIcon, previousConfig, previousInfo := read(icon), read(config), read("Info.plist")
	previousMetadata := readMetadata()
	verifySignature := func() {
		t.Helper()
		if output, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--deep", path).CombinedOutput(); err != nil {
			t.Fatalf("macOS signature: %v (%s)", err, output)
		}
	}
	broken := backend
	signErr := errors.New("signing interrupted")
	broken.sign = func(context.Context, string) error { return signErr }
	bundles.backend = broken
	if err := bundles.RefreshRuntime(t.Context(), path); !errors.Is(err, signErr) || !bytes.Equal(read(marker), legacy.runtimeDigest) || !bytes.Equal(read("Info.plist"), previousInfo) {
		t.Fatalf("failed update replaced the committed bundle: %v", err)
	}
	verifySignature()

	bundles.backend = backend
	if updated, err := service.Path(t.Context(), saved, nil); err != nil || updated != path || !bytes.Equal(read(marker), backend.runtimeDigest) {
		t.Fatalf("refresh on use = %q, %v", updated, err)
	}
	matchedDirectory := fileInfo(t, path)
	if !os.SameFile(appDirectory, matchedDirectory) || os.SameFile(originalContents, fileInfo(t, contents)) {
		t.Fatal("the refresh should keep the .app directory and replace the complete signed Contents")
	}
	if !matchedDirectory.ModTime().After(appDirectory.ModTime()) {
		t.Fatal("the bundle modification date must let Launch Services detect the new Contents")
	}
	checkMediaUsageDescriptions(t, plist)
	currentMetadata := readMetadata()
	delete(currentMetadata, "NSCameraUsageDescription")
	delete(currentMetadata, "NSMicrophoneUsageDescription")
	if !bytes.Equal(read(icon), previousIcon) || !bytes.Equal(read(config), previousConfig) || !reflect.DeepEqual(currentMetadata, previousMetadata) {
		t.Fatal("runtime refresh changed the author's icon, settings or bundle identity")
	}
	verifySignature()
	after := fileInfo(t, contents)
	if err := bundles.RefreshRuntime(t.Context(), path); err != nil || !os.SameFile(after, fileInfo(t, contents)) {
		t.Fatalf("current runtime was unnecessarily replaced: %v", err)
	}
	executable := filepath.Join("MacOS", darwinRunner)
	truncatedSize := len(read(executable)) / 2
	if err := os.Truncate(filepath.Join(contents, executable), int64(truncatedSize)); err != nil {
		t.Fatal(err)
	}
	if updated, err := service.Path(t.Context(), saved, nil); err != nil || updated != path ||
		len(read(executable)) <= truncatedSize || !bytes.Equal(read(marker), backend.runtimeDigest) {
		t.Fatalf("an incomplete executable with a current version marker did not repair at %q (%d bytes): %v", updated, len(read(executable)), err)
	}
	if !os.SameFile(appDirectory, fileInfo(t, path)) || !bytes.Equal(read(icon), previousIcon) || !bytes.Equal(read(config), previousConfig) {
		t.Fatal("repairing the executable changed the application directory, icon or settings")
	}
	verifySignature()
	if apps, err := service.List(); err != nil || len(apps) != 1 || apps[0] != saved {
		t.Fatalf("settings changed during refresh: %+v, %v", apps, err)
	}
}

func TestMacRestoresCompleteBackupForMatchingRevision(t *testing.T) {
	bundles, service := releaseFixture(t)
	app, err := service.Save(t.Context(), profile.App{Name: "Saved app", URL: "https://example.org"}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join("Contents", "MacOS", darwinRunner)
	original := fileInfo(t, filepath.Join(path, executable))
	staging := NewLibrary(t.TempDir(), bundles.backend)
	replacement, err := staging.Install(t.Context(), profile.App{}, app, icons.Artwork{Image: icons.Brand()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(replacement, executable)); err != nil {
		t.Fatal(err)
	}
	dir, backup := bundles.paths(app.ID)
	if err := os.Rename(dir, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(replacement), dir); err != nil {
		t.Fatal(err)
	}
	bundles.root, bundles.priorRoots = filepath.Join(filepath.Dir(bundles.root), "Creator"), []string{bundles.root}
	restored, err := service.Path(t.Context(), app, nil)
	if err != nil || restored != path {
		t.Fatalf("app restored from backup = %q, %v", restored, err)
	}
	if !os.SameFile(original, fileInfo(t, filepath.Join(restored, executable))) {
		t.Fatal("the complete backup was replaced instead of restored")
	}
}

func TestMacSettingsKeepDirectoryAcrossSaveAndRecovery(t *testing.T) {
	for _, test := range []struct {
		name, newName string
		legacy        bool
		interrupt     bool
		commit        bool
	}{
		{name: "failed resize", newName: "Workbench", legacy: true},
		{name: "interrupted legacy rename before profile write", newName: "Research", legacy: true, interrupt: true},
		{name: "interrupted rename after profile write", newName: "Research", interrupt: true, commit: true},
		{name: "case-only rename", newName: "WORKBENCH", commit: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundles, _ := releaseFixture(t)
			backend := bundles.backend.(darwinBackend)
			store := profile.NewStore(filepath.Join(t.TempDir(), "apps.json"))
			app := profile.App{ID: profile.NewID(), Name: "Workbench", URL: "https://example.org", Width: 680, Height: 490}
			path, err := bundles.Install(t.Context(), profile.App{}, app, icons.Artwork{Image: icons.Brand()}, func() error { return store.Put(app) })
			if err != nil {
				t.Fatal(err)
			}
			dir, backup := bundles.paths(app.ID)
			if test.legacy {
				if err := os.Rename(backend.revisionPath(path), filepath.Join(dir, revisionName)); err != nil {
					t.Fatal(err)
				}
				if err := signBundle(t.Context(), path); err != nil {
					t.Fatal(err)
				}
			}
			identity := fileInfo(t, path)
			next := app
			next.Name, next.Width, next.WindowRevision = test.newName, 750, 1
			storageError := errors.New("profile write failed")
			interruption := errors.New("interrupted after publishing the staged bundle")
			func() {
				if test.interrupt {
					defer func() {
						if err := recover(); err != interruption {
							t.Fatalf("expected an interrupted save, got %v", err)
						}
					}()
				}
				_, err := bundles.UpdateSettings(t.Context(), app, next, nil, func() error {
					if test.commit {
						if err := store.Put(next); err != nil {
							return err
						}
					}
					if test.interrupt {
						panic(interruption)
					}
					if !test.commit {
						return storageError
					}
					return nil
				})
				if (test.commit && err != nil) || (!test.commit && !errors.Is(err, storageError)) {
					t.Fatalf("save returned %v", err)
				}
			}()
			want := app
			if test.commit {
				want = next
			}
			saved, err := store.Find(app.ID)
			if err != nil || saved != want {
				t.Fatalf("saved profile = %+v, %v; want %+v", saved, err, want)
			}
			restarted := NewLibrary(bundles.root, backend)
			restored, err := restarted.Path(saved)
			if err != nil || restored != filepath.Join(dir, backend.Filename(want)) {
				t.Fatalf("the saved app did not reopen: %q, %v", restored, err)
			}
			if !os.SameFile(identity, fileInfo(t, restored)) {
				t.Fatal("saving or recovering settings replaced the original application directory")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() && entry.Name() != backend.Filename(want) {
					t.Fatalf("unexpected bundle name after recovery: %q", entry.Name())
				}
			}
			if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a resolved transaction kept its backup: %v", err)
			}
			if out, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--deep", restored).CombinedOutput(); err != nil {
				t.Fatalf("recovered signature: %v (%s)", err, out)
			}
		})
	}
}

func TestMacSettingsKeepInstalledIconAndRepairMissingIcon(t *testing.T) {
	bundles, service := releaseFixture(t)
	bundles.priorRoots = []string{filepath.Join(filepath.Dir(bundles.root), "WebCrate")}
	app, err := service.Save(t.Context(), profile.App{Name: "Desk", URL: "https://example.org"}, library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}})
	if err != nil {
		t.Fatal(err)
	}
	path, err := bundles.Path(app)
	if err != nil {
		t.Fatal(err)
	}
	iconPath := func(appPath string) string { return filepath.Join(appPath, "Contents", "Resources", "AppIcon.icns") }
	before, err := os.ReadFile(iconPath(path))
	if err != nil {
		t.Fatal(err)
	}
	update := func(draft profile.App, fallback image.Image) (profile.App, string) {
		t.Helper()
		saved, err := service.Save(t.Context(), draft, library.Icon{Artwork: icons.Artwork{Image: fallback}, KeepInstalled: true})
		if err != nil {
			t.Fatal(err)
		}
		path, err := bundles.Path(saved)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--deep", path).CombinedOutput(); err != nil {
			t.Fatalf("macOS signature after editing: %v (%s)", err, out)
		}
		return saved, path
	}
	fallback := icons.Fallback("M", "#df5b48")
	app.Name, app.Width = "Renamed", 860
	app, renamed := update(app, fallback)
	if data, err := os.ReadFile(iconPath(renamed)); err != nil || !bytes.Equal(data, before) {
		t.Fatalf("editing replaced the installed icon with its fallback: %v", err)
	}
	if err := os.Remove(iconPath(renamed)); err != nil {
		t.Fatal(err)
	}
	app.Width = 960
	app, path = update(app, fallback)
	want, err := icns(icons.Artwork{Image: fallback})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(iconPath(path)); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("editing did not repair the missing icon: %v", err)
	}
	if err := os.Truncate(iconPath(path), 0); err != nil {
		t.Fatal(err)
	}
	if same, repaired := update(app, fallback); same != app || repaired != path {
		t.Fatalf("repair after unchanged save changed the app: %+v, %s", same, repaired)
	}
	if data, err := os.ReadFile(iconPath(path)); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("unchanged save did not repair the empty icon: %v", err)
	}
	// Damaged files that retain their original size must also be repaired.
	damaged := append([]byte(nil), want...)
	damaged[0] ^= 1
	if err := os.WriteFile(iconPath(path), damaged, 0o644); err != nil {
		t.Fatal(err)
	}
	if same, repaired := update(app, fallback); same != app || repaired != path {
		t.Fatalf("repair of the damaged icon changed the application: %+v, %s", same, repaired)
	}
	if data, err := os.ReadFile(iconPath(path)); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("unchanged save did not repair the damaged icon: %v", err)
	}
	if err := os.WriteFile(iconPath(path), damaged, 0o644); err != nil {
		t.Fatal(err)
	}
	app.Width += 40
	app, path = update(app, fallback)
	if data, err := os.ReadFile(iconPath(path)); err != nil || !bytes.Equal(data, want) {
		t.Fatalf("saving new settings copied the damaged icon: %v", err)
	}
	configPath := filepath.Join(path, "Contents", "Resources", ConfigName)
	incorrect := app
	incorrect.URL += "/uncommitted"
	if err := writeConfig(configPath, incorrect, iconMetadata{}); err != nil {
		t.Fatal(err)
	}
	if found, err := bundles.Path(app); found != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("matching revision masked the incorrect settings: %q, %v", found, err)
	}
	if same, repaired := update(app, fallback); same != app || repaired != path {
		t.Fatalf("repair of the settings changed the application: %+v, %s", same, repaired)
	}
}

func BenchmarkMacInstalledPath(b *testing.B) {
	for _, source := range []string{"Current", "Legacy"} {
		b.Run(source, func(b *testing.B) {
			bundles, service := releaseFixture(b)
			icon := library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}}
			app, err := service.Save(context.Background(), profile.App{Name: "Example", URL: "https://example.com"}, icon)
			if err != nil {
				b.Fatal(err)
			}
			want, err := bundles.Path(app)
			if err != nil {
				b.Fatal(err)
			}
			root := filepath.Dir(bundles.root)
			bundles.priorRoots = []string{filepath.Join(root, "WebCrate"), filepath.Join(root, "LegacyConfig", "Applications")}
			if source == "Legacy" {
				bundles.priorRoots[0], bundles.root = bundles.root, filepath.Join(root, "Creator")
			}
			b.ReportAllocs()
			for b.Loop() {
				if path, err := bundles.Path(app); err != nil || path != want {
					b.Fatalf("installed path = %q, %v, want %q", path, err, want)
				}
			}
		})
	}
}

func BenchmarkMacCheckCurrentRuntime(b *testing.B) {
	bundles, service := releaseFixture(b)
	app, err := service.Save(b.Context(), profile.App{Name: "Installed app", URL: "https://example.com"}, library.Icon{})
	if err != nil {
		b.Fatal(err)
	}
	path, err := bundles.Path(app)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := bundles.RefreshRuntime(b.Context(), path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMacCreate(b *testing.B) {
	_, service := releaseFixture(b)
	icon := icons.Brand()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		app, err := service.Save(ctx, profile.App{Name: "Example", URL: "https://example.com"}, library.Icon{Artwork: icons.Artwork{Image: icon}})
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err := service.Remove(app.ID); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func BenchmarkMacSaveSettings(b *testing.B) {
	for _, edit := range []string{"Unchanged", "Resize", "Rename"} {
		b.Run(edit, func(b *testing.B) {
			_, service := releaseFixture(b)
			ctx := context.Background()
			icon := library.Icon{Artwork: icons.Artwork{Image: icons.Brand()}}
			app, err := service.Save(ctx, profile.App{Name: "Example", URL: "https://example.com"}, icon)
			if err != nil {
				b.Fatal(err)
			}
			icon.KeepInstalled = true
			b.ReportAllocs()
			for b.Loop() {
				switch edit {
				case "Resize":
					app.Width = 2100 - app.Width
				case "Rename":
					if app.Name == "Example" {
						app.Name = "Desk"
					} else {
						app.Name = "Example"
					}
				}
				app, err = service.Save(ctx, app, icon)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestMacStandaloneLoadsInSystemWebKit(t *testing.T) {
	if os.Getenv("WEBCRATE_GUI_SMOKE") != "1" {
		t.Skip("requires a logged-in macOS desktop and loopback networking")
	}
	checkStandaloneSessions(t, func(path string) string {
		return filepath.Join(path, "Contents", "MacOS", darwinRunner)
	})
}

func TestMacWebsiteWindowInteractions(t *testing.T) {
	if os.Getenv("WEBCRATE_GUI_SMOKE") != "1" {
		t.Skip("requires a logged-in macOS desktop")
	}
	source := filepath.Join("..", "..", "runners", "macos")
	compiler := []string{"clang", "-fobjc-arc", "-mmacosx-version-min=12.0", "-framework", "AppKit", "-framework", "WebKit"}
	probeDir := t.TempDir()
	var profile string
	if root := os.Getenv("WEBCRATE_MAC_COVERAGE"); root != "" {
		var err error
		probeDir, err = os.MkdirTemp(root, "window-")
		if err != nil {
			t.Fatal(err)
		}
		profile = filepath.Join(probeDir, "window-%p.profraw")
		compiler = append(compiler, "-fprofile-instr-generate", "-fcoverage-mapping")
		t.Logf("Native coverage files: %s", probeDir)
	}
	probe := filepath.Join(probeDir, "window-test")
	compiler = append(compiler, filepath.Join(source, "window_test.m"), filepath.Join(source, "downloads.m"), filepath.Join(source, "settings.m"), "-o", probe)
	if output, err := exec.CommandContext(t.Context(), "xcrun", compiler...).CombinedOutput(); err != nil {
		t.Fatalf("build native window regression: %v (%s)", err, output)
	}
	mux := http.NewServeMux()
	opening := make(chan struct{})
	var releaseOpening sync.Once
	mux.HandleFunc("GET /release-opening", func(w http.ResponseWriter, _ *http.Request) {
		releaseOpening.Do(func() { close(opening) })
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("GET /workspace", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-opening:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Test site</title>
<style>body{margin:0;background:#cce6cc}#files{position:fixed;top:4px;left:4px}</style>
<input type="file" id="files" multiple><input type="file" id="directory" webkitdirectory hidden>
<a id="attachment" href="/attachment" hidden>Download export</a>
<a id="navigation" href="/navigation?source=menu" hidden>Navigate</a>
<a id="recover-link" href="/recover/link?source=retry" hidden>Recover</a>`))
	})
	type attachmentGate struct {
		once sync.Once
		done chan struct{}
	}
	attachments := make(map[string]*attachmentGate)
	for _, mode := range []string{"replace", "interrupted", "changed", "new-interrupted", "new-collision", "retry", "parallel-first", "parallel-second", "quit"} {
		attachments[mode] = &attachmentGate{done: make(chan struct{})}
	}
	mux.HandleFunc("GET /attachment", func(w http.ResponseWriter, r *http.Request) {
		const document = "Exported website file\n"
		mode := r.URL.Query().Get("mode")
		gate := attachments[mode]
		var prefix string
		if gate != nil {
			prefix = strings.Repeat("x", 32*1024)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Disposition", `attachment; filename="workbench.txt"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(prefix)+len(document)))
		if gate != nil {
			_, _ = w.Write([]byte(prefix))
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
			select {
			case <-gate.done:
			case <-r.Context().Done():
				return
			}
			if mode == "interrupted" || mode == "new-interrupted" {
				// The response ends with part of its advertised body missing.
				return
			}
		}
		_, _ = w.Write([]byte(document))
	})
	mux.HandleFunc("GET /release-attachment/{mode}", func(w http.ResponseWriter, r *http.Request) {
		gate := attachments[r.PathValue("mode")]
		if gate == nil {
			http.NotFound(w, r)
			return
		}
		gate.once.Do(func() { close(gate.done) })
		_, _ = w.Write([]byte("ready"))
	})
	var visits atomic.Int64
	mux.HandleFunc("GET /navigation", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "source=menu" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`<!doctype html><title>Navigation ` + strconv.FormatInt(visits.Add(1), 10) + `</title>`))
	})
	mux.HandleFunc("GET /popup", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Sign-in fixture</title>`))
	})
	var restored sync.Map
	mux.HandleFunc("GET /restore/{test}", func(w http.ResponseWriter, request *http.Request) {
		restored.Store(request.PathValue("test"), true)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("GET /recover/{test}", func(w http.ResponseWriter, request *http.Request) {
		if _, ready := restored.Load(request.PathValue("test")); !ready {
			connection, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Errorf("interrupt the test website's connection: %v", err)
				return
			}
			_ = connection.Close()
			return
		}
		if request.URL.RawQuery != "source=retry" {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Recovered site</title><h1>Recovered</h1>`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	files := t.TempDir()
	for name, content := range map[string]string{"one.txt": "first", "two.txt": "second"} {
		if err := os.WriteFile(filepath.Join(files, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	home := t.TempDir()
	run := exec.CommandContext(ctx, probe, server.URL, files, home)
	run.Env = append(os.Environ(), "HOME="+home)
	if profile != "" {
		run.Env = append(run.Env, "LLVM_PROFILE_FILE="+profile)
	}
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("native website interaction: %v (%s)", err, output)
	}
	if len(output) > 0 {
		t.Logf("%s", bytes.TrimSpace(output))
	}
}
