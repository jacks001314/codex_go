package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func securitySetupTestModel() *Model {
	return NewModel(nil, Options{Width: 100, Height: 28})
}

func securitySetupTestNotice() *SecuritySetupNotice {
	return &SecuritySetupNotice{
		Title:       "Secure your account",
		Description: "Add a second factor to protect this account.",
		ActionLabel: "Open settings",
		ActionURL:   "https://chatgpt.com/security",
	}
}

func TestSecuritySetupBannerDismissAndActionLikeRust(t *testing.T) {
	model := securitySetupTestModel()
	model.securitySetupRequestID = 3
	model.applySecuritySetupResult(SecuritySetupResultMsg{
		RequestID: 3,
		Identity:  "account|user",
		Notice:    securitySetupTestNotice(),
	})
	if !model.securitySetupBannerVisible() {
		t.Fatal("security setup banner is not visible")
	}
	if view := model.View(); !strings.Contains(view, "Secure your account") ||
		!strings.Contains(view, "Open settings") {
		t.Fatalf("view missing security setup banner:\n%s", view)
	}

	var opened string
	model.onOpenURL = func(target string) error {
		opened = target
		return nil
	}
	updated, command := model.Update(keyRunes('1'))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("action key returned no command")
	}
	_ = command()
	if opened != "https://chatgpt.com/security" {
		t.Fatalf("opened = %q", opened)
	}
	if model.securitySetupBannerVisible() {
		t.Fatal("action did not dismiss the banner")
	}

	model.securitySetupDismissed = false
	model.securitySetupNotice = securitySetupTestNotice()
	model.Update(key(bubbletea.KeyEsc))
	if model.securitySetupBannerVisible() || !model.securitySetupDismissed {
		t.Fatal("Escape did not dismiss the banner")
	}
}

func TestSecuritySetupStaleIdentityAndBannerPriorityLikeRust(t *testing.T) {
	model := securitySetupTestModel()
	model.securitySetupRequestID = 9
	model.applySecuritySetupResult(SecuritySetupResultMsg{
		RequestID: 8,
		Identity:  "old|user",
		Notice:    securitySetupTestNotice(),
	})
	if model.securitySetupNotice != nil {
		t.Fatal("stale result was applied")
	}

	model.applySecuritySetupResult(SecuritySetupResultMsg{
		RequestID: 9,
		Identity:  "account|user",
		Notice:    securitySetupTestNotice(),
	})
	model.securitySetupDismissed = true
	model.securitySetupNotice = securitySetupTestNotice()
	model.applySecuritySetupResult(SecuritySetupResultMsg{
		RequestID: 9,
		Identity:  "account|user",
		Notice:    securitySetupTestNotice(),
	})
	if model.securitySetupNotice != nil {
		t.Fatal("dismissed occurrence was restored for the same identity")
	}
	model.applySecuritySetupResult(SecuritySetupResultMsg{
		RequestID: 9,
		Identity:  "other|user",
		Notice:    securitySetupTestNotice(),
	})
	if model.securitySetupDismissed || model.securitySetupNotice == nil {
		t.Fatal("a new identity did not reset dismissal")
	}

	account := "account"
	model.backendBanner.banner = &BackendBannerView{
		BannerType:  BackendBannerLunaReserve,
		AccountID:   account,
		Dismissible: true,
	}
	if model.securitySetupBannerVisible() {
		t.Fatal("usage banner did not take priority")
	}
}
