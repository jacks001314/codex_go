package chatwidget

import appsapi "codex_go/internal/apps"

type ChatWidgetConnectors struct {
	AppServer      bool
	RemoteControl  bool
	Notifications  bool
	Clipboard      bool
	ExternalEditor bool
}

func (c ChatWidgetConnectors) ReadyForInteractiveSession() bool {
	return c.AppServer || c.RemoteControl
}

func (c ChatWidgetConnectors) CanPostNotification() bool {
	return c.Notifications
}

type ConnectorsCacheKind string

const (
	ConnectorsCacheUninitialized ConnectorsCacheKind = "uninitialized"
	ConnectorsCacheLoading       ConnectorsCacheKind = "loading"
	ConnectorsCacheReady         ConnectorsCacheKind = "ready"
	ConnectorsCacheFailed        ConnectorsCacheKind = "failed"
)

type ConnectorsSnapshot struct {
	Connectors []appsapi.AppEntry
}

type ConnectorsCacheState struct {
	Kind     ConnectorsCacheKind
	Snapshot ConnectorsSnapshot
	Error    string
}

type ConnectorsState struct {
	Cache                  ConnectorsCacheState
	PartialSnapshot        *ConnectorsSnapshot
	PrefetchInFlight       bool
	ForceRefetchPending    bool
	LastBottomPaneSnapshot *ConnectorsSnapshot
	LastView               *SelectionView
}

type ConnectorsOutputKind string

const (
	ConnectorsOutputDisabled ConnectorsOutputKind = "disabled"
	ConnectorsOutputEmpty    ConnectorsOutputKind = "empty"
	ConnectorsOutputPopup    ConnectorsOutputKind = "popup"
	ConnectorsOutputLoading  ConnectorsOutputKind = "loading"
	ConnectorsOutputFailed   ConnectorsOutputKind = "failed"
)

type ConnectorsOutputResult struct {
	Kind          ConnectorsOutputKind
	View          *SelectionView
	InfoMessage   string
	Hint          string
	ErrorMessage  string
	FetchQueued   bool
	ForceRefetch  bool
	RequestRedraw bool
}

type ConnectorsLoadResult struct {
	Snapshot ConnectorsSnapshot
	Error    string
}

type ConnectorsLoadOutcome struct {
	View                       *SelectionView
	BottomPaneSnapshot         *ConnectorsSnapshot
	TriggerPendingForceRefetch bool
	Failed                     bool
}

func NewConnectorsCacheReady(snapshot ConnectorsSnapshot) ConnectorsCacheState {
	return ConnectorsCacheState{Kind: ConnectorsCacheReady, Snapshot: cloneConnectorsSnapshot(snapshot)}
}

func NewConnectorsCacheFailed(message string) ConnectorsCacheState {
	return ConnectorsCacheState{Kind: ConnectorsCacheFailed, Error: message}
}

func (s *ConnectorsState) BeginRefresh(enabled bool, forceRefetch bool) bool {
	if s == nil || !enabled {
		return false
	}
	if s.PrefetchInFlight {
		if forceRefetch {
			s.ForceRefetchPending = true
		}
		return false
	}
	s.PrefetchInFlight = true
	if s.Cache.Kind != ConnectorsCacheReady {
		s.Cache = ConnectorsCacheState{Kind: ConnectorsCacheLoading}
	}
	return true
}

func (s *ConnectorsState) Prefetch(enabled bool) bool {
	return s.BeginRefresh(enabled, false)
}

func (s *ConnectorsState) Refresh(enabled bool, forceRefetch bool) bool {
	return s.BeginRefresh(enabled, forceRefetch)
}

func (s *ConnectorsState) ConnectorsForMentions(enabled bool) ([]appsapi.AppEntry, bool) {
	if s == nil || !enabled {
		return nil, false
	}
	if s.PartialSnapshot != nil {
		return cloneAppEntries(s.PartialSnapshot.Connectors), true
	}
	if s.Cache.Kind == ConnectorsCacheReady {
		return cloneAppEntries(s.Cache.Snapshot.Connectors), true
	}
	return nil, false
}

func (s *ConnectorsState) AddOutput(enabled bool) ConnectorsOutputResult {
	if s == nil {
		return ConnectorsOutputResult{}
	}
	if !enabled {
		return ConnectorsOutputResult{
			Kind:        ConnectorsOutputDisabled,
			InfoMessage: "Apps are disabled.",
			Hint:        "Enable the apps feature to use $ or /apps.",
		}
	}
	cache := s.Cache
	shouldForceRefetch := !s.PrefetchInFlight || cache.Kind == ConnectorsCacheReady
	fetchQueued := s.BeginRefresh(enabled, shouldForceRefetch)
	result := ConnectorsOutputResult{
		FetchQueued:   fetchQueued,
		ForceRefetch:  shouldForceRefetch,
		RequestRedraw: true,
	}
	switch cache.Kind {
	case ConnectorsCacheReady:
		if len(cache.Snapshot.Connectors) == 0 {
			result.Kind = ConnectorsOutputEmpty
			result.InfoMessage = "No apps available."
			return result
		}
		view := NewConnectorsCatalogView(cache.Snapshot, "")
		s.LastView = &view
		result.Kind = ConnectorsOutputPopup
		result.View = &view
		return result
	case ConnectorsCacheFailed:
		result.Kind = ConnectorsOutputFailed
		result.ErrorMessage = cache.Error
		return result
	default:
		view := AppsLoadingView()
		s.LastView = &view
		result.Kind = ConnectorsOutputLoading
		result.View = &view
		return result
	}
}

func (s *ConnectorsState) OnLoaded(result ConnectorsLoadResult, final bool, selectedIndex int) ConnectorsLoadOutcome {
	if s == nil {
		return ConnectorsLoadOutcome{}
	}
	outcome := ConnectorsLoadOutcome{}
	if final {
		s.PrefetchInFlight = false
		if s.ForceRefetchPending {
			s.ForceRefetchPending = false
			outcome.TriggerPendingForceRefetch = true
		}
	}

	if result.Error == "" {
		snapshot := cloneConnectorsSnapshot(result.Snapshot)
		if s.Cache.Kind == ConnectorsCacheReady {
			snapshot.Connectors = preserveConnectorEnabledState(snapshot.Connectors, s.Cache.Snapshot.Connectors)
		}
		if final {
			selectedID := connectorIDAt(s.Cache.Snapshot.Connectors, selectedIndex)
			s.PartialSnapshot = nil
			s.Cache = NewConnectorsCacheReady(snapshot)
			view := NewConnectorsCatalogView(snapshot, selectedID)
			s.LastView = &view
			outcome.View = &view
		} else {
			s.PartialSnapshot = &snapshot
		}
		s.LastBottomPaneSnapshot = &snapshot
		outcome.BottomPaneSnapshot = &snapshot
		return outcome
	}

	outcome.Failed = true
	partial := s.PartialSnapshot
	s.PartialSnapshot = nil
	if s.Cache.Kind == ConnectorsCacheReady {
		snapshot := cloneConnectorsSnapshot(s.Cache.Snapshot)
		s.LastBottomPaneSnapshot = &snapshot
		outcome.BottomPaneSnapshot = &snapshot
		return outcome
	}
	if partial != nil {
		snapshot := cloneConnectorsSnapshot(*partial)
		s.Cache = NewConnectorsCacheReady(snapshot)
		view := NewConnectorsCatalogView(snapshot, connectorIDAt(snapshot.Connectors, selectedIndex))
		s.LastView = &view
		s.LastBottomPaneSnapshot = &snapshot
		outcome.View = &view
		outcome.BottomPaneSnapshot = &snapshot
		return outcome
	}
	s.Cache = NewConnectorsCacheFailed(result.Error)
	s.LastBottomPaneSnapshot = nil
	return outcome
}

func (s *ConnectorsState) UpdateConnectorEnabled(connectorID string, enabled bool) bool {
	if s == nil || s.Cache.Kind != ConnectorsCacheReady {
		return false
	}
	snapshot := cloneConnectorsSnapshot(s.Cache.Snapshot)
	changed := false
	for i := range snapshot.Connectors {
		if snapshot.Connectors[i].ID == connectorID {
			changed = snapshot.Connectors[i].IsEnabled != enabled
			snapshot.Connectors[i].IsEnabled = enabled
			break
		}
	}
	if !changed {
		return false
	}
	s.Cache = NewConnectorsCacheReady(snapshot)
	view := NewConnectorsCatalogView(snapshot, connectorID)
	s.LastView = &view
	s.LastBottomPaneSnapshot = &snapshot
	return true
}

func NewConnectorsCatalogView(snapshot ConnectorsSnapshot, selectedConnectorID string) SelectionView {
	response := appsapi.AppListResponse{Data: cloneAppEntries(snapshot.Connectors)}
	view := NewAppsCatalogView(response)
	if selectedConnectorID != "" {
		for i := range view.Items {
			if view.Items[i].ID == selectedConnectorID {
				view.InitialSelectedIndex = i
				break
			}
		}
	}
	return view
}

func ConnectorStatusLabel(app appsapi.AppEntry) string {
	if app.IsAccessible {
		if app.IsEnabled {
			return "Installed"
		}
		return "Installed \u2022 Disabled"
	}
	return "Can be installed"
}

func ConnectorBriefDescription(app appsapi.AppEntry) string {
	status := ConnectorStatusLabel(app)
	if desc := AppDescription(app); desc != "" {
		return status + " \u2022 " + desc
	}
	return status
}

func preserveConnectorEnabledState(next []appsapi.AppEntry, existing []appsapi.AppEntry) []appsapi.AppEntry {
	enabledByID := map[string]bool{}
	for _, connector := range existing {
		enabledByID[connector.ID] = connector.IsEnabled
	}
	out := cloneAppEntries(next)
	for i := range out {
		if enabled, ok := enabledByID[out[i].ID]; ok {
			out[i].IsEnabled = enabled
		}
	}
	return out
}

func connectorIDAt(connectors []appsapi.AppEntry, index int) string {
	if index < 0 || index >= len(connectors) {
		return ""
	}
	return connectors[index].ID
}

func cloneConnectorsSnapshot(snapshot ConnectorsSnapshot) ConnectorsSnapshot {
	return ConnectorsSnapshot{Connectors: cloneAppEntries(snapshot.Connectors)}
}

func cloneAppEntries(entries []appsapi.AppEntry) []appsapi.AppEntry {
	out := append([]appsapi.AppEntry(nil), entries...)
	for i := range out {
		if out[i].Description != nil {
			value := *out[i].Description
			out[i].Description = &value
		}
		if out[i].InstallURL != nil {
			value := *out[i].InstallURL
			out[i].InstallURL = &value
		}
		out[i].Labels = append([]string(nil), out[i].Labels...)
		out[i].PluginDisplayNames = append([]string(nil), out[i].PluginDisplayNames...)
	}
	return out
}
