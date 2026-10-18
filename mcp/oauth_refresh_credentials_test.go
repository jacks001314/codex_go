package mcp

import "testing"

func TestMCPOAuthRefreshCredentialsLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		config     *ServerConfig
		stored     *OAuthTokenSet
		wantID     string
		wantSecret string
		wantOmit   bool
		wantErr    bool
	}{
		{
			name:       "stored dynamic credentials",
			stored:     &OAuthTokenSet{ClientID: "dynamic-client", ClientSecret: "dynamic-secret"},
			wantID:     "dynamic-client",
			wantSecret: "dynamic-secret",
		},
		{
			name:       "configured confidential client wins",
			config:     &ServerConfig{OAuthClientID: "configured-client", OAuthClientSecret: "configured-secret"},
			stored:     &OAuthTokenSet{ClientID: "configured-client", ClientSecret: "stale-secret"},
			wantID:     "configured-client",
			wantSecret: "configured-secret",
			wantOmit:   true,
		},
		{
			name:    "configured client id change requires login",
			config:  &ServerConfig{OAuthClientID: "configured-client", OAuthClientSecret: "configured-secret"},
			stored:  &OAuthTokenSet{ClientID: "other-client", ClientSecret: "stored-secret"},
			wantErr: true,
		},
		{
			name:     "configured client id without stored id",
			config:   &ServerConfig{OAuthClientID: "configured-client"},
			wantID:   "configured-client",
			wantOmit: false,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			id, secret, omit, err := mcpOAuthRefreshCredentials(testCase.config, testCase.stored)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected a reauthentication error")
				}
				return
			}
			if err != nil {
				t.Fatalf("mcpOAuthRefreshCredentials() error = %v", err)
			}
			if id != testCase.wantID || secret != testCase.wantSecret || omit != testCase.wantOmit {
				t.Fatalf("credentials = (%q, %q, %t), want (%q, %q, %t)", id, secret, omit, testCase.wantID, testCase.wantSecret, testCase.wantOmit)
			}
		})
	}
}
