package provisionservice

import (
	"errors"
	"os"
	"strings"
)

// PackageFamilyEnv routes an unpackaged client to a packaged service channel.
// Service admission still verifies OS identity.
const PackageFamilyEnv = "CODEX_WINDOWS_SANDBOX_PACKAGE_FAMILY"

const (
	legacyServiceName    = "CodexSandboxService"
	packageFamilyNameMax = 50
	packagePublisherLen  = 13
)

// ValidateServiceFamilyHint mirrors Rust validate_service_family_hint. Only
// lookups are routed by the hint; authentication never trusts it.
func ValidateServiceFamilyHint(family string) error {
	index := strings.LastIndex(family, "_")
	if index < 0 {
		return errors.New("invalid service package family")
	}
	name, publisher := family[:index], family[index+1:]
	if name == "" || len(name) > packageFamilyNameMax {
		return errors.New("invalid service package family")
	}
	for _, char := range name {
		if !isPackageNameChar(char) {
			return errors.New("invalid service package family")
		}
	}
	if len(publisher) != packagePublisherLen {
		return errors.New("invalid service package family")
	}
	for _, char := range publisher {
		if !isASCIIDigitOrLetter(char) {
			return errors.New("invalid service package family")
		}
	}
	return nil
}

// ServiceName returns the SCM name for the active package family.
func ServiceName() (string, error) {
	family, ok, err := servicePackageFamily()
	if err != nil {
		return "", err
	}
	if !ok {
		return legacyServiceName, nil
	}
	index := strings.LastIndex(family, "_")
	if index < 0 {
		return "", errors.New("invalid package family")
	}
	return legacyServiceName + "." + family[:index], nil
}

// ServicePipeName returns the authenticated pipe for the active family.
func ServicePipeName() (string, error) {
	family, ok, err := servicePackageFamily()
	if err != nil {
		return "", err
	}
	if !ok {
		return PipeName, nil
	}
	return PipeName + "." + family, nil
}

func servicePackageFamily() (string, bool, error) {
	family, ok, err := currentPackageFamily()
	if err != nil {
		return "", false, err
	}
	if ok {
		return family, true, nil
	}
	hint, ok := os.LookupEnv(PackageFamilyEnv)
	if !ok {
		return "", false, nil
	}
	if err := ValidateServiceFamilyHint(hint); err != nil {
		return "", false, err
	}
	return hint, true, nil
}

func isPackageNameChar(char rune) bool {
	return isASCIIDigitOrLetter(char) || char == '.' || char == '-'
}

func isASCIIDigitOrLetter(char rune) bool {
	return (char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
}
