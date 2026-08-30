package mcp

import "testing"

func TestPluginAttributionFromRegistration(t *testing.T) {
	tests := []struct {
		name string
		reg  *ServerRegistration
		want *PluginAttribution
	}{
		{
			name: "nil registration",
			reg:  nil,
			want: nil,
		},
		{
			name: "no plugin ID",
			reg: &ServerRegistration{
				Name:   "test",
				Source: "config",
			},
			want: nil,
		},
		{
			name: "with plugin ID and display name",
			reg: &ServerRegistration{
				Name:              "test",
				Source:            "plugin",
				PluginID:          "acme/weather",
				PluginDisplayName: "Weather Plugin",
				PluginHostRoot:    "file:///plugins/acme",
			},
			want: &PluginAttribution{
				PluginID:    "acme/weather",
				DisplayName: "Weather Plugin",
				HostRoot:    "file:///plugins/acme",
			},
		},
		{
			name: "with plugin ID, no display name",
			reg: &ServerRegistration{
				Name:     "test",
				Source:   "plugin",
				PluginID: "acme/weather",
			},
			want: &PluginAttribution{
				PluginID:    "acme/weather",
				DisplayName: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PluginAttributionFromRegistration(tt.reg)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if got != nil && tt.want != nil {
				if got.PluginID != tt.want.PluginID || got.DisplayName != tt.want.DisplayName || got.HostRoot != tt.want.HostRoot {
					t.Errorf("got %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

func TestResolvedServerIncludesPluginAttribution(t *testing.T) {
	actions := []CatalogAction{
		{
			Name:   "weather",
			Source: CatalogSourcePlugin,
			PluginAttribution: &PluginAttribution{
				PluginID:    "acme/weather",
				DisplayName: "Weather Plugin",
			},
			Config: ServerConfig{
				Command: "weather-server",
				Enabled: true,
			},
			RegistrationOrder: 0,
		},
	}

	catalog := ResolveCatalog(actions)

	server, ok := catalog.Servers["weather"]
	if !ok {
		t.Fatal("expected weather server in catalog")
	}

	if server.PluginAttribution == nil {
		t.Fatal("expected plugin attribution")
	}

	if server.PluginAttribution.PluginID != "acme/weather" {
		t.Errorf("plugin ID = %q", server.PluginAttribution.PluginID)
	}

	if server.PluginAttribution.DisplayName != "Weather Plugin" {
		t.Errorf("display name = %q", server.PluginAttribution.DisplayName)
	}
}

func TestResolvedServerNoPluginAttributionForConfig(t *testing.T) {
	actions := []CatalogAction{
		{
			Name:   "weather",
			Source: CatalogSourceConfig,
			Config: ServerConfig{
				Command: "weather-server",
				Enabled: true,
			},
			RegistrationOrder: 0,
		},
	}

	catalog := ResolveCatalog(actions)

	server, ok := catalog.Servers["weather"]
	if !ok {
		t.Fatal("expected weather server in catalog")
	}

	if server.PluginAttribution != nil {
		t.Errorf("expected no plugin attribution for config source, got %+v", server.PluginAttribution)
	}
}

func TestDisabledRegistrationIsNameVeto(t *testing.T) {
	tests := []struct {
		source CatalogSource
		want   bool
	}{
		{CatalogSourcePlugin, true},
		{CatalogSourceSelectedPlugin, false},
		{CatalogSourceConfig, true},
		{CatalogSourceCompatibility, true},
		{CatalogSourceExtension, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			got := DisabledRegistrationIsNameVeto(tt.source)
			if got != tt.want {
				t.Errorf("DisabledRegistrationIsNameVeto(%q) = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}
