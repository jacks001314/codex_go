package remotecontrol

import "testing"

func TestHostDeviceKindFromProfileLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		want    string
		ok      bool
	}{
		{
			name:    "mac mini",
			profile: `{"SPHardwareDataType":[{"machine_name":"Mac mini","chip_type":"Apple M4"}]}`,
			want:    "mac_mini",
			ok:      true,
		},
		{
			name:    "other machine name",
			profile: `{"SPHardwareDataType":[{"machine_name":"MacBook Pro"}]}`,
			want:    "",
			ok:      true,
		},
		{
			name:    "empty profile",
			profile: `{"SPHardwareDataType":[]}`,
			want:    "",
			ok:      true,
		},
		{
			name:    "malformed profile",
			profile: `not-json`,
			want:    "",
			ok:      false,
		},
		{
			name:    "case-sensitive machine name",
			profile: `{"SPHardwareDataType":[{"machine_name":"mac mini"}]}`,
			want:    "",
			ok:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := hostDeviceKindFromProfile([]byte(tc.profile))
			if ok != tc.ok || got != tc.want {
				t.Fatalf("hostDeviceKindFromProfile() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestBuildRemoteControlWebsocketRequestOmitsHostDeviceHeaderWhenUnknown(t *testing.T) {
	token := "tok"
	request, err := BuildRemoteControlWebsocketRequest(
		"wss://example.com/remote-control",
		&Enrollment{ServerID: "server-1", ServerName: "name", RemoteControlToken: &token},
		"install-1",
		nil,
	)
	if err != nil {
		t.Fatalf("BuildRemoteControlWebsocketRequest() error = %v", err)
	}
	if kind := request.Header.Get(RemoteControlHostDeviceKindHeader); kind != "" {
		t.Fatalf("host device kind header = %q, want empty", kind)
	}
}
