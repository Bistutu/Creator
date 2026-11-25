package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPortableRunnerDoesNotLoadCreatorPackages(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", "-tags", "mygo_nodownload,mygo_noinspector", "../../cmd/runner")
			cmd.Env = append(os.Environ(), "GOOS="+platform, "GOARCH=amd64", "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("list runner dependencies: %v\n%s", err, out)
			}
			for _, dependency := range strings.Fields(string(out)) {
				switch dependency {
				case "github.com/Bistutu/Creator/internal/bundle",
					"github.com/Bistutu/Creator/internal/icons",
					"github.com/Bistutu/Creator/internal/launcher",
					"github.com/Bistutu/Creator/internal/library":
					t.Errorf("website launcher depends on Creator package %s", dependency)
				}
			}
		})
	}
}

func TestRunBuildsEachRunnerOnceAndForwardsReleaseOptions(t *testing.T) {
	resources := t.TempDir()
	host := target{"darwin", "arm64"}
	targets := []target{host, {"darwin", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}}
	compileCalls := 0
	releaseCalls := 0
	invoke := func(env []string, name string, args ...string) error {
		if name == "go" && args[0] == "tool" {
			releaseCalls++
			want := []string{"tool", "mygo", "build", "-platform", "darwin/arm64,darwin/universal,windows/arm64,linux/amd64", "-skip-dmg", "-o", "build/release"}
			if !slices.Equal(args, want) || len(env) != 0 {
				t.Fatalf("MyGo command = %v, %v", env, args)
			}
			for _, runner := range targets {
				assertRunner(t, resources, runner, runner.String())
				assertRunnerDigest(t, resources, runner, runner.String())
			}
			return nil
		}

		if compileCalls == len(targets) {
			t.Fatalf("unexpected extra compiler call %s %v", name, args)
		}
		runner := targets[compileCalls]
		compileCalls++
		output := compilerOutput(t, args)
		dir := filepath.Dir(filepath.Dir(output))
		if filepath.Dir(dir) != resources || !strings.HasPrefix(filepath.Base(dir), ".runners-") {
			t.Fatalf("compiler output %s is outside the staging directory", output)
		}
		if _, err := os.Stat(filepath.Join(resources, runner.runnerPath())); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runner was published during compilation: %v", err)
		}
		if runner.os == "darwin" {
			arch := runner.arch
			if arch == "amd64" {
				arch = "x86_64"
			}
			if name != "xcrun" || !slices.Equal(args[:3], []string{"clang", "-arch", arch}) {
				t.Fatalf("clang command = %s %v", name, args)
			}
		} else {
			wantEnv := []string{"GOOS=" + runner.os, "GOARCH=" + runner.arch, "CGO_ENABLED=0"}
			flags := args[slices.Index(args, "-ldflags")+1]
			if name != "go" || args[0] != "build" || !slices.Equal(env, wantEnv) ||
				strings.Contains(flags, "-H=windowsgui") != (runner.os == "windows") {
				t.Fatalf("Go compiler command = %v, %s %v", env, name, args)
			}
		}
		return os.WriteFile(output, []byte(runner.String()), 0o755)
	}
	args := []string{"-platform", "darwin/arm64, darwin/universal, windows/arm64, linux/amd64, windows/arm64", "--", "-skip-dmg", "-o", "build/release"}
	if err := run(args, resources, host, io.Discard, invoke); err != nil {
		t.Fatal(err)
	}
	if compileCalls != len(targets) || releaseCalls != 1 {
		t.Fatalf("got %d compiler calls and %d release calls", compileCalls, releaseCalls)
	}
	assertNoStaging(t, resources)
}

func TestRunPrepareDefaultsToHost(t *testing.T) {
	resources := t.TempDir()
	host := target{"linux", "arm64"}
	invoke := func(env []string, name string, args ...string) error {
		if name != "go" || args[0] != "build" || !slices.Equal(env, []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0"}) {
			t.Fatalf("unexpected command %s %v, %v", name, args, env)
		}
		return os.WriteFile(compilerOutput(t, args), []byte("native runner"), 0o755)
	}
	if err := run([]string{"-prepare"}, resources, host, io.Discard, invoke); err != nil {
		t.Fatal(err)
	}
	assertRunner(t, resources, host, "native runner")
	assertRunnerDigest(t, resources, host, "native runner")
}

func TestRunValidatesAllTargetsBeforeCompiling(t *testing.T) {
	for _, test := range []struct {
		platforms, message string
	}{
		{"linux/arm64,freebsd/amd64", "unsupported target"},
		{"linux/arm64,windows/universal", "unsupported target"},
		{"linux/arm64,", "unsupported target"},
		{"linux/arm64,darwin/universal", "macOS helpers require a Mac"},
	} {
		t.Run(test.platforms, func(t *testing.T) {
			resources := filepath.Join(t.TempDir(), "resources")
			invoke := func([]string, string, ...string) error {
				t.Fatal("validation reached a compiler")
				return nil
			}
			err := run([]string{"-platform", test.platforms}, resources, target{"linux", "amd64"}, io.Discard, invoke)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want %q", err, test.message)
			}
			if _, err := os.Stat(resources); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid command touched resources: %v", err)
			}
		})
	}
}

func TestRunKeepsExistingRunnersWhenPreparationFails(t *testing.T) {
	failed := errors.New("compiler failed")
	for _, test := range []struct {
		name, message string
		output        func(string) error
		blockPublish  string
	}{
		{name: "compiler failure", message: "compiler failed", output: func(string) error { return failed }},
		{name: "missing output", message: "read linux/amd64 helper", output: func(string) error { return nil }},
		{name: "empty output", message: "invalid linux/amd64 helper", output: func(path string) error { return os.WriteFile(path, nil, 0o755) }},
		{name: "blocked destination", message: "invalid linux/amd64 helper destination", blockPublish: "runner", output: func(path string) error {
			return os.WriteFile(path, []byte("new runner"), 0o755)
		}},
		{name: "blocked checksum", message: "invalid linux/amd64 helper destination", blockPublish: "checksum", output: func(path string) error {
			return os.WriteFile(path, []byte("new runner"), 0o755)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources := t.TempDir()
			host := target{"linux", "arm64"}
			prior := filepath.Join(resources, host.runnerPath())
			if err := os.MkdirAll(filepath.Dir(prior), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(prior, []byte("previous runner"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeRunnerDigest(prior); err != nil {
				t.Fatal(err)
			}
			if test.blockPublish != "" {
				blocked := filepath.Join(resources, target{"linux", "amd64"}.runnerPath())
				if test.blockPublish == "checksum" {
					blocked += ".sha256"
				}
				if err := os.MkdirAll(blocked, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			invoke := func(env []string, name string, args ...string) error {
				if name != "go" || args[0] != "build" {
					t.Fatalf("release was invoked after a broken preparation: %s %v", name, args)
				}
				output := compilerOutput(t, args)
				if slices.Contains(env, "GOARCH=amd64") {
					return test.output(output)
				}
				return os.WriteFile(output, []byte("new runner"), 0o755)
			}
			err := run([]string{"-platform", "linux/arm64,linux/amd64"}, resources, host, io.Discard, invoke)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want %q", err, test.message)
			}
			assertRunner(t, resources, host, "previous runner")
			assertRunnerDigest(t, resources, host, "previous runner")
			assertNoStaging(t, resources)
		})
	}
}

func compilerOutput(t *testing.T, args []string) string {
	t.Helper()
	index := slices.Index(args, "-o")
	if index < 0 || index+1 >= len(args) {
		t.Fatalf("missing compiler output argument in %v", args)
	}
	return args[index+1]
}

func assertRunner(t *testing.T, resources string, runner target, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(resources, runner.runnerPath()))
	if err != nil || string(data) != want {
		t.Fatalf("installed %s helper = %q, %v", runner, data, err)
	}
}

func assertRunnerDigest(t *testing.T, resources string, runner target, contents string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(resources, runner.runnerPath()+".sha256"))
	if runner.os == "darwin" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected %s checksum: %q, %v", runner, data, err)
		}
		return
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(contents)))
	if err != nil || string(data) != want {
		t.Fatalf("%s checksum = %q, %v, want %q", runner, data, err, want)
	}
}

func assertNoStaging(t *testing.T, resources string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(resources, ".runners-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staged helpers remain: %v, %v", matches, err)
	}
}
