package bundle

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os/exec"

	"github.com/Bistutu/Creator/internal/profile"
)

const cameraDescription = "网站在视频通话或拍摄时需要使用摄像头。"
const microphoneDescription = "网站在语音输入或通话时需要使用麦克风。"

func infoPlist(app profile.App) []byte {
	var name bytes.Buffer
	_ = xml.EscapeText(&name, []byte(app.Name))
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleIdentifier</key><string>%s</string>
  <key>CFBundleDisplayName</key><string>%s</string>
  <key>CFBundleName</key><string>%s</string>
  <key>CFBundleExecutable</key><string>WebCrateRunner</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSCameraUsageDescription</key><string>%s</string>
  <key>NSMicrophoneUsageDescription</key><string>%s</string>
  <key>NSAppTransportSecurity</key><dict>
    <key>NSAllowsArbitraryLoadsInWebContent</key><true/>
  </dict>
</dict></plist>`, darwinBundlePrefix+app.ID, name.Bytes(), name.Bytes(), cameraDescription, microphoneDescription)
}

func addMediaDescriptions(ctx context.Context, path string, original []byte) error {
	for _, field := range []struct{ key, description string }{
		{"NSCameraUsageDescription", cameraDescription},
		{"NSMicrophoneUsageDescription", microphoneDescription},
	} {
		// The normal generated XML already contains the usage strings. Use
		// the system's plist editor for older bundles to retain other keys.
		if bytes.Contains(original, []byte("<key>"+field.key+"</key>")) {
			continue
		}
		out, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-replace", field.key, "-string", field.description, path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("macOS 无法更新应用授权说明：%w\n%s", err, out)
		}
	}
	return nil
}
