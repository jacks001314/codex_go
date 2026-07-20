package appserver

import (
	"encoding/json"
	"testing"

	"codex_go/apps"
)

func TestAppInstalledReturnsCommittedAccessibleRuntimeState(t *testing.T) {
	service := apps.NewAppService([]apps.AppEntry{
		{ID: "callable", Name: "Callable", IsAccessible: true, IsEnabled: true},
		{ID: "disabled", Name: "Disabled", IsAccessible: true, IsEnabled: false},
		{ID: "directory-only", Name: "Directory", IsAccessible: false, IsEnabled: true},
	})
	router := NewRuntimeRouter(RuntimeServices{Apps: service})
	response := router.Handle(requestWithParams(t, IntID(1), MethodAppInstalled, apps.AppsInstalledParams{}))
	if response.Error != nil {
		t.Fatalf("app/installed error = %#v", response.Error)
	}
	result := response.Result.(*apps.AppsInstalledResponse)
	if len(result.Apps) != 2 || result.Apps[0].ID != "callable" || !result.Apps[0].Callable || result.Apps[1].ID != "disabled" || result.Apps[1].Callable {
		t.Fatalf("app/installed result = %#v", result.Apps)
	}
}

func TestAppInstalledJSONUsesRequiredArrayAndNullableRuntimeName(t *testing.T) {
	data, err := json.Marshal(&apps.AppsInstalledResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"apps":[]}` {
		t.Fatalf("json = %s", data)
	}
	data, err = json.Marshal(apps.InstalledApp{ID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"id":"app","runtimeName":null,"enabled":false,"callable":false}` {
		t.Fatalf("installed app json = %s", data)
	}
}
