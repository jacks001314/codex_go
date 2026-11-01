//go:build windows

package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The Windows desktop update check mirrors Rust's updates::append_desktop_update:
// it fetches the production appcast, records the CDN result on the reachability
// check and adds the update rows when the manifest names a newer build.

const (
	// Windows reads the Store manifest; macOS uses the appcast feeds instead.
	desktopUpdateEntryURL      = "https://persistent.oaistatic.com/codex-app-prod/windows-store-update.json"
	desktopStoreProductID      = "9PLM9XGG6VKS"
	desktopStorePackageName    = "OpenAI.Codex"
	desktopUpdateTimeout       = 2 * time.Second
	desktopUpdateMaxManifest   = 1024 * 1024
	desktopUpdateCheckID       = "updates.status"
	desktopReachabilityCheckID = "network.provider_reachability"
)

func desktopUpdatesSupported() bool { return true }

var desktopUpdateURLProbe = func() string { return desktopUpdateEntryURL }

// applyDesktopUpdateDetails appends the desktop rows Rust derives from the
// production appcast. It reports the CDN outcome on the reachability check even
// when no desktop application is installed.
func applyDesktopUpdateDetails(checks []*DoctorCheck, client *http.Client) {
	reachability := doctorCheckByID(checks, desktopReachabilityCheckID)
	if reachability == nil || client == nil {
		return
	}
	app, err := desktopAppProbe()
	if err != nil || app == nil {
		return
	}
	url := desktopUpdateURLProbe()
	displayURL, _, _ := strings.Cut(url, "?")
	ctx, cancel := context.WithTimeout(context.Background(), desktopUpdateTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		desktopUpdateFailure(reachability, displayURL, err.Error())
		return
	}
	response, err := client.Do(request)
	if err != nil {
		desktopUpdateFailure(reachability, displayURL, desktopRequestErrorText(err))
		return
	}
	defer response.Body.Close()
	status := response.StatusCode
	scheme := ""
	if response.Request != nil && response.Request.URL != nil {
		scheme = response.Request.URL.Scheme
	}
	if status == http.StatusNotFound && scheme == "https" {
		reachability.Detail("desktop assets CDN: " + displayURL + " reachable (HTTP 404; no update available)")
		return
	}
	if scheme != "https" {
		desktopUpdateFailure(reachability, displayURL, "update manifest redirected to a non-HTTPS URL")
		return
	}
	if status == http.StatusProxyAuthRequired {
		desktopUpdateFailure(reachability, displayURL, "proxy authentication required (HTTP 407)")
		return
	}
	if status < 200 || status > 299 {
		desktopUpdateFailure(reachability, displayURL, "HTTP "+strconv.Itoa(status))
		return
	}
	reachability.Detail("desktop assets CDN: " + displayURL + " reachable (HTTP " + strconv.Itoa(status) + ")")
	updates := doctorCheckByID(checks, desktopUpdateCheckID)
	if updates == nil {
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, desktopUpdateMaxManifest))
	if readErr != nil {
		updates.Status = worseCheckStatus(updates.Status, CheckStatusWarning)
		updates.Detail("desktop update manifest: response could not be read")
		return
	}
	build, newer, manifestErr := windowsStoreUpdate(body, app.version)
	if manifestErr != nil {
		updates.Status = worseCheckStatus(updates.Status, CheckStatusWarning)
		updates.Detail("desktop update manifest: " + manifestErr.Error())
		return
	}
	if !newer {
		return
	}
	updates.Detail("desktop update status: available").
		Detail("desktop latest build: " + build).
		Detail("desktop application: " + app.identity)
}

func desktopUpdateFailure(reachability *DoctorCheck, displayURL string, reason string) {
	reachability.Detail("desktop assets CDN: " + displayURL + " " + reason + " (optional)")
	if reachability.Status == CheckStatusOK {
		reachability.Status = CheckStatusWarning
		reachability.Summary = "desktop update and runtime CDN is unreachable"
	}
	measured := displayURL + " " + reason
	expected := "desktop update CDN reachable over HTTPS"
	remedy := "check proxy, firewall, DNS, and certificate access to persistent.oaistatic.com"
	reachability.Issues = append(reachability.Issues, &DoctorIssue{
		Severity: CheckStatusWarning,
		Cause:    "desktop update and runtime CDN is unreachable",
		Measured: &measured,
		Expected: &expected,
		Remedy:   &remedy,
		Fields:   []string{"desktop assets CDN"},
	})
}

// desktopRequestErrorText renders transport errors the way Rust's
// network::request_error does.
func desktopRequestErrorText(err error) string {
	text := err.Error()
	if strings.Contains(text, "context deadline exceeded") {
		return "request timed out"
	}
	return text
}

// windowsStoreUpdate decodes the production Store manifest and reports whether
// its build is newer than the installed one.
func windowsStoreUpdate(manifest []byte, installedVersion string) (string, bool, error) {
	var parsed struct {
		SchemaVersion   uint64 `json:"schemaVersion"`
		BuildVersion    string `json:"buildVersion"`
		StoreProductID  string `json:"storeProductId"`
		PackageIdentity string `json:"packageIdentity"`
	}
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return "", false, errors.New("invalid Windows Store update manifest")
	}
	if parsed.SchemaVersion == 0 || parsed.StoreProductID != desktopStoreProductID || parsed.PackageIdentity != desktopStorePackageName {
		return "", false, errors.New("Windows Store update manifest does not target the production application")
	}
	latest, ok := desktopBuildComponents(parsed.BuildVersion)
	if !ok {
		return "", false, errors.New("Windows Store update manifest contains an invalid build version")
	}
	installed, ok := desktopBuildComponents(installedVersion)
	if !ok {
		return "", false, errors.New("installed Windows application has an invalid version")
	}
	return parsed.BuildVersion, desktopBuildNewer(latest, installed), nil
}

func desktopBuildComponents(version string) ([4]uint64, bool) {
	parts := strings.Split(version, ".")
	if len(parts) != 4 {
		return [4]uint64{}, false
	}
	var components [4]uint64
	for index, part := range parts {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return [4]uint64{}, false
		}
		components[index] = value
	}
	return components, true
}

func desktopBuildNewer(latest [4]uint64, installed [4]uint64) bool {
	for index := range latest {
		if latest[index] != installed[index] {
			return latest[index] > installed[index]
		}
	}
	return false
}

func doctorCheckByID(checks []*DoctorCheck, id string) *DoctorCheck {
	for _, check := range checks {
		if check != nil && check.ID == id {
			return check
		}
	}
	return nil
}
