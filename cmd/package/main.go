// Command package builds the runtime helpers before MyGo assembles Creator.
package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

func main() {
	host := target{runtime.GOOS, runtime.GOARCH}
	if err := run(os.Args[1:], "resources", host, os.Stderr, command); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

type target struct {
	os   string
	arch string
}

func (t target) String() string {
	return t.os + "/" + t.arch
}

func (t target) runnerPath() string {
	name := "WebCrateRunner"
	if t.os == "windows" {
		name += ".exe"
	}
	return filepath.Join(t.os+"-"+t.arch, name)
}

func (t target) runnerFiles() []string {
	path := t.runnerPath()
	if t.os == "darwin" {
		return []string{path}
	}
	return []string{path, path + ".sha256"}
}

type buildCommand func(env []string, name string, args ...string) error

func run(args []string, resources string, host target, stderr io.Writer, invoke buildCommand) error {
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	flags.SetOutput(stderr)
	platforms := flags.String("platform", host.String(), "comma separated OS/architecture targets")
	prepare := flags.Bool("prepare", false, "prepare the helpers for go run or mygo dev")
	if err := flags.Parse(args); err != nil {
		return err
	}
	release, runners, err := planTargets(*platforms, host.os)
	if err != nil {
		return err
	}
	if err := prepareRunners(resources, runners, invoke); err != nil {
		return err
	}
	if *prepare {
		return nil
	}
	return invoke(nil, "go", append([]string{"tool", "mygo", "build", "-platform", strings.Join(release, ",")}, flags.Args()...)...)
}

func planTargets(platforms, hostOS string) (release []string, runners []target, err error) {
	for _, platform := range strings.Split(platforms, ",") {
		platform = strings.TrimSpace(platform)
		osName, arch, _ := strings.Cut(platform, "/")
		if osName != "darwin" && osName != "windows" && osName != "linux" {
			return nil, nil, fmt.Errorf("unsupported target %q", platform)
		}
		if arch != "arm64" && arch != "amd64" && !(osName == "darwin" && arch == "universal") {
			return nil, nil, fmt.Errorf("unsupported target %q", platform)
		}
		if osName == "darwin" && hostOS != "darwin" {
			return nil, nil, errors.New("macOS helpers require a Mac with the Xcode command line tools")
		}
		if !slices.Contains(release, platform) {
			release = append(release, platform)
		}
		architectures := []string{arch}
		if arch == "universal" {
			architectures = []string{"arm64", "amd64"}
		}
		for _, arch := range architectures {
			runner := target{osName, arch}
			if !slices.Contains(runners, runner) {
				runners = append(runners, runner)
			}
		}
	}
	return release, runners, nil
}

func prepareRunners(resources string, runners []target, invoke buildCommand) error {
	if err := os.MkdirAll(resources, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(resources, ".runners-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for _, runner := range runners {
		output := filepath.Join(stage, runner.runnerPath())
		if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
			return err
		}
		if err := runner.compile(output, invoke); err != nil {
			return fmt.Errorf("compile %s helper: %w", runner, err)
		}
		info, err := os.Lstat(output)
		if err != nil {
			return fmt.Errorf("read %s helper: %w", runner, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("compiler produced an invalid %s helper", runner)
		}
		if runner.os != "darwin" {
			if err := writeRunnerDigest(output); err != nil {
				return fmt.Errorf("write %s helper checksum: %w", runner, err)
			}
		}
	}
	for _, runner := range runners {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(resources, runner.runnerPath())), 0o755); err != nil {
			return err
		}
		for _, name := range runner.runnerFiles() {
			path := filepath.Join(resources, name)
			info, err := os.Lstat(path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect %s helper destination: %w", runner, err)
			}
			if err == nil && !info.Mode().IsRegular() {
				return fmt.Errorf("invalid %s helper destination %s", runner, path)
			}
		}
	}
	for _, runner := range runners {
		if runner.os != "darwin" {
			// An interrupted developer build can leave a runner without a
			// checksum. Creator can identify that runner on first use.
			digest := filepath.Join(resources, runner.runnerPath()+".sha256")
			if err := os.Remove(digest); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		for _, path := range runner.runnerFiles() {
			if err := os.Rename(filepath.Join(stage, path), filepath.Join(resources, path)); err != nil {
				return fmt.Errorf("publish %s helper: %w", runner, err)
			}
		}
	}
	return nil
}

func writeRunnerDigest(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	return os.WriteFile(path+".sha256", fmt.Appendf(nil, "%x", digest.Sum(nil)), 0o644)
}

func (t target) compile(output string, invoke buildCommand) error {
	if t.os == "darwin" {
		appleArch := t.arch
		if t.arch == "amd64" {
			appleArch = "x86_64"
		}
		return invoke(nil, "xcrun", "clang", "-arch", appleArch, "-fobjc-arc", "-Os", "-Wall", "-Wextra", "-Wno-unused-parameter",
			"-mmacosx-version-min=12.0", "-framework", "AppKit", "-framework", "WebKit",
			"runners/macos/main.m", "runners/macos/downloads.m", "runners/macos/settings.m", "-o", output)
	}
	ldflags := "-s -w"
	if t.os == "windows" {
		ldflags += " -H=windowsgui"
	}
	return invoke([]string{"GOOS=" + t.os, "GOARCH=" + t.arch, "CGO_ENABLED=0"}, "go", "build", "-trimpath", "-tags", "mygo_nodownload,mygo_noinspector", "-ldflags", ldflags, "-o", output, "./cmd/runner")
}

func command(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
