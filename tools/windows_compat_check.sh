#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
  printf 'Usage: %s WINE_BINARY FIXTURE_DIRECTORY\n' "$0" >&2
  exit 2
fi
if [ "$(uname -s):$(uname -m)" != Linux:aarch64 ]; then
  printf 'This check runs in the headless Linux ARM64 guest.\n' >&2
  exit 2
fi

export WINELOADER
WINELOADER=$(realpath -e "$1")
export WINEDLLPATH
WINEDLLPATH=$(dirname "$WINELOADER")
export WINESERVER="$WINEDLLPATH/wineserver"
test -x "$WINELOADER"
test -x "$WINESERVER"

cd "$2"
runner=resources/windows-arm64/WebCrateRunner.exe
for fixture in launcher.test.exe bundle.test.exe "$runner" "$runner.sha256"; do
  if [ ! -s "$fixture" ]; then
    printf 'Missing Windows ARM64 fixture: %s\n' "$fixture" >&2
    exit 2
  fi
done
printf '%s  %s\n' "$(cat "$runner.sha256")" "$runner" | sha256sum --check --strict
mkdir -p internal/bundle
scratch=$(mktemp -d "$PWD/.wine-check.XXXXXX")
export WINEPREFIX="$scratch/prefix"
export WINEDLLOVERRIDES='mscoree,mshtml,winemenubuilder.exe=' WINEDEBUG=-all GOMAXPROCS=1

cleanup() {
  timeout -k 2 4 "$WINESERVER" -k >/dev/null 2>&1 || true
  rm -rf -- "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# The ASCII selection applies to the lifecycle subtests. Native Windows CI
# runs both lifecycle cases, including Unicode.
timeout -k 4 60 xvfb-run -a -s '-screen 0 640x480x24 -nolisten tcp' sh -eu -c '
  "$WINELOADER" ./launcher.test.exe -test.v -test.run "^TestWindows(StartMenu|MenuDirectory|Shortcut)/^ASCII$"
  cd internal/bundle
  WEBCRATE_GUI_SMOKE=1 "$WINELOADER" ../../bundle.test.exe -test.v -test.run "^(TestPortableCreatesAndManagesStandaloneApplicationOffline|TestPortableRepairsMissingExecutableWithInstalledArtwork|TestWindowsRepairsMissingIconFormatUsingInstalledArtwork|TestApplicationNamesKeepPortableResourcesIntact|TestRuntime(LinksAndUpdatesOneAppAtATime|ChecksAnInstalledVersionUsingThePackageDigest|CurrentChecksContinueDuringRepair|DoesNotPublishBytesThatDisagreeWithThePackageDigest|ReplacesAnOversizedMarkerAndKeepsAConflictingDirectory)|TestPortableRunnerSettings.*|TestWindowsPortableSettings.*)$"
'
