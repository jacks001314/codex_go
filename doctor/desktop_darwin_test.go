//go:build darwin

package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeDesktopBundleFixture(t *testing.T, dictionary string, binary bool) string {
	t.Helper()
	bundle := filepath.Join(t.TempDir(), "Codex.app")
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(contents, "Info.plist")
	plist := "<?xml version=\"1.0\"?><plist version=\"1.0\"><dict>" + dictionary + "</dict></plist>"
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	if binary {
		if output, err := exec.Command("/usr/bin/plutil", "-convert", "binary1", path).CombinedOutput(); err != nil {
			t.Fatalf("convert binary fixture: %v: %s", err, output)
		}
	}
	return bundle
}

func TestDesktopPlatformReadsXMLAndBinaryPlistsLikeRust(t *testing.T) {
	for _, binary := range []bool{false, true} {
		name := "xml"
		if binary {
			name = "binary"
		}
		t.Run(name, func(t *testing.T) {
			bundle := writeDesktopBundleFixture(t,
				"<key>CFBundleIdentifier</key><string>com.openai.codex</string>"+
					"<key>CFBundleShortVersionString</key><string>1.2&amp;canary</string>"+
					"<key>CFBundleVersion</key><string>18446744073709551615</string>", binary)
			app, err := desktopInspectBundle(bundle)
			if err != nil || app == nil {
				t.Fatalf("inspect bundle: app=%+v err=%v", app, err)
			}
			if app.identity != desktopMacBundleIdentifier || app.bundle != bundle || app.version != "1.2&canary" {
				t.Fatalf("application = %+v", app)
			}
		})
	}
}

func TestDesktopPlatformValidatesBundleMetadataLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fields    string
		version   string
		wantError bool
		unrelated bool
	}{
		{name: "explicit plus build", fields: "<key>CFBundleVersion</key><string>+42</string>", version: "+42"},
		{name: "fallback", fields: "<key>CFBundleVersion</key><string>42</string>", version: "42"},
		{name: "empty short version", fields: "<key>CFBundleShortVersionString</key><string></string><key>CFBundleVersion</key><string>42</string>"},
		{name: "numeric short version", fields: "<key>CFBundleShortVersionString</key><integer>12</integer><key>CFBundleVersion</key><string>42</string>", wantError: true},
		{name: "missing build", fields: "<key>CFBundleShortVersionString</key><string>1</string>", wantError: true},
		{name: "numeric build", fields: "<key>CFBundleVersion</key><integer>42</integer>", wantError: true},
		{name: "invalid build", fields: "<key>CFBundleVersion</key><string>not-a-build</string>", wantError: true},
		{name: "overflow build", fields: "<key>CFBundleVersion</key><string>18446744073709551616</string>", wantError: true},
		{name: "unrelated app", fields: "<key>CFBundleIdentifier</key><string>example.other</string>", unrelated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := tc.fields
			if !tc.unrelated {
				fields = "<key>CFBundleIdentifier</key><string>com.openai.codex</string>" + fields
			}
			app, err := desktopInspectBundle(writeDesktopBundleFixture(t, fields, false))
			if tc.wantError {
				if err != errDesktopBundle || app != nil {
					t.Fatalf("app=%+v err=%v, want discovery error", app, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.unrelated {
				if app != nil {
					t.Fatalf("unrelated app = %+v", app)
				}
			} else if app == nil || app.version != tc.version {
				t.Fatalf("app=%+v, want version %q", app, tc.version)
			}
		})
	}
}

func TestDesktopPlatformRejectsOversizedPlistOutput(t *testing.T) {
	bundle := writeDesktopBundleFixture(t,
		"<key>CFBundleIdentifier</key><string>com.openai.codex</string>"+
			"<key>Extra</key><string>"+strings.Repeat("x", 70*1024)+"</string>", false)
	if _, err := desktopInspectBundle(bundle); err != errDesktopBundle {
		t.Fatalf("oversized metadata error = %v, want discovery error", err)
	}
}

func TestDesktopPlatformIgnoresNonDictionaryPlistLikeRust(t *testing.T) {
	bundle := writeDesktopBundleFixture(t, "", false)
	path := filepath.Join(bundle, "Contents", "Info.plist")
	if err := os.WriteFile(path, []byte("<?xml version=\"1.0\"?><plist version=\"1.0\"><array><string>unrelated</string></array></plist>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if app, err := desktopInspectBundle(bundle); app != nil || err != nil {
		t.Fatalf("non-dictionary plist app=%+v err=%v, want unrelated bundle", app, err)
	}
}
