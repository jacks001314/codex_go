package chatgptapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// VoiceSettings is the ChatGPT account-owned Codex voice preference and catalog.
type VoiceSettings struct {
	Selected string        `json:"selected"`
	Voices   []VoiceOption `json:"voices"`
}

// VoiceOption is one account voice choice. Name may be absent on older backends.
type VoiceOption struct {
	Voice string  `json:"voice"`
	Name  *string `json:"name"`
}

// GetVoiceSettings reads the account voice preference used by Codex voice chat.
func (c *CloudClient) GetVoiceSettings(ctx context.Context) (*VoiceSettings, error) {
	if c == nil {
		return nil, errors.New("cloud client is nil")
	}
	query := url.Values{"voice_mode": []string{"wingman"}}
	var settings VoiceSettings
	if err := c.doJSON(ctx, http.MethodGet, "/settings/voices", query, nil, &settings); err != nil {
		return nil, err
	}
	if settings.Voices == nil {
		settings.Voices = []VoiceOption{}
	}
	return &settings, nil
}

// SetVoiceSettings saves the account voice preference used by Codex voice chat.
func (c *CloudClient) SetVoiceSettings(ctx context.Context, voice string) error {
	if c == nil {
		return errors.New("cloud client is nil")
	}
	query := url.Values{
		"feature": []string{"voice_name"},
		"value":   []string{voice},
	}
	return c.doJSON(ctx, http.MethodPatch, "/settings/account_user_setting", query, nil, nil)
}
