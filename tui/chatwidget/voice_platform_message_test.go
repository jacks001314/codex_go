package chatwidget

import "testing"

func TestCheckVoiceCommandAvailabilityUsesSpecificPlatformMessage(t *testing.T) {
	specific := "Voice runtime is not installed for this build (codex-resources/voice helper not found)."
	context := VoiceCommandContext{
		FeatureEnabled:    true,
		PlatformSupported: false,
		PlatformMessage:   specific,
	}
	result := CheckVoiceCommandAvailability(context)
	if result.Allowed || result.Message != specific {
		t.Fatalf("availability = %#v, want the specific runtime message", result)
	}
	context.PlatformMessage = ""
	if result := CheckVoiceCommandAvailability(context); result.Message != "Voice requires macOS, an MSVC-based Windows build, or a glibc-based Linux build." {
		t.Fatalf("default platform message = %q", result.Message)
	}
}
