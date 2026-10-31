package windowsandboxservice

import (
	"path/filepath"
	"testing"
)

func TestRequireRuntimePackageFamilyMatchesInstalledFamilyOnly(t *testing.T) {
	service := "OpenAI.Codex_3k8sg7r9htsxt"
	beta := "OpenAI.CodexBeta_3k8sg7r9htsxt"
	otherPublisher := "OpenAI.Codex_otherpublisher"
	cases := []struct {
		name    string
		client  *string
		service *string
		wantErr bool
	}{
		{"same family", &service, &service, false},
		{"different package", &beta, &service, true},
		{"different publisher", &otherPublisher, &service, true},
		{"unpackaged client", nil, &service, true},
		{"unpackaged service", &service, nil, true},
		{"both unpackaged", nil, nil, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := RequireRuntimePackageFamily(testCase.client, testCase.service)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("RequireRuntimePackageFamily(%v, %v) error = %v, wantErr %v", testCase.client, testCase.service, err, testCase.wantErr)
			}
			if err != nil && err.Error() != "runtime registration requires a client in the service's installed package family" {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

func TestInstalledServiceImageAdmitsOnlyPackagedEntryPoints(t *testing.T) {
	serviceDirectory := filepath.Join("C:", "Program Files", "WindowsApps", "OpenAI.Codex_3k8sg7r9htsxt", "app")
	cases := []struct {
		name  string
		image string
		want  bool
	}{
		{"codex", filepath.Join(serviceDirectory, "codex.exe"), true},
		{"code mode host", filepath.Join(serviceDirectory, "codex-code-mode-host.exe"), true},
		{"case insensitive", filepath.Join(serviceDirectory, "CODEX.EXE"), true},
		{"other helper", filepath.Join(serviceDirectory, "codex-sandbox.exe"), false},
		{"outside the service directory", filepath.Join("C:", "Users", "owner", "codex.exe"), false},
		{"prefix lookalike", serviceDirectory + "-beta" + filepath.Join("", "codex.exe"), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := isInstalledServiceImage(testCase.image, serviceDirectory); got != testCase.want {
				t.Fatalf("isInstalledServiceImage(%q) = %v, want %v", testCase.image, got, testCase.want)
			}
		})
	}
}
