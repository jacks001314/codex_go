package remotecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrInvalidRequest = errors.New("invalid remote-control request")

type EnableParams struct {
	Ephemeral bool `json:"ephemeral,omitempty"`
}

type DisableParams struct {
	Ephemeral bool `json:"ephemeral,omitempty"`
}

type ConnectionStatus string

const (
	StatusDisabled   ConnectionStatus = "disabled"
	StatusConnecting ConnectionStatus = "connecting"
	StatusConnected  ConnectionStatus = "connected"
	StatusErrored    ConnectionStatus = "errored"
)

type StatusChangedNotification struct {
	Status         ConnectionStatus `json:"status"`
	ServerName     string           `json:"serverName"`
	InstallationID string           `json:"installationId"`
	EnvironmentID  *string          `json:"environmentId"`
}

type EnableResponse struct {
	Status         ConnectionStatus `json:"status"`
	ServerName     string           `json:"serverName"`
	InstallationID string           `json:"installationId"`
	EnvironmentID  *string          `json:"environmentId"`
}

type DisableResponse struct {
	Status         ConnectionStatus `json:"status"`
	ServerName     string           `json:"serverName"`
	InstallationID string           `json:"installationId"`
	EnvironmentID  *string          `json:"environmentId"`
}

type StatusReadResponse struct {
	Status         ConnectionStatus `json:"status"`
	ServerName     string           `json:"serverName"`
	InstallationID string           `json:"installationId"`
	EnvironmentID  *string          `json:"environmentId"`
}

type PairingStartParams struct {
	ManualCode bool `json:"manualCode,omitempty"`
}

type PairingStartResponse struct {
	PairingCode       string  `json:"pairingCode"`
	ManualPairingCode *string `json:"manualPairingCode"`
	EnvironmentID     string  `json:"environmentId"`
	ExpiresAt         int64   `json:"expiresAt"`
}

type PairingStatusParams struct {
	PairingCode       *string `json:"pairingCode,omitempty"`
	ManualPairingCode *string `json:"manualPairingCode,omitempty"`
}

func (p *PairingStatusParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidRequest)
	}
	if stringValue(p.PairingCode) == "" && stringValue(p.ManualPairingCode) == "" {
		return fmt.Errorf("%w: pairingCode or manualPairingCode is required", ErrInvalidRequest)
	}
	if stringValue(p.PairingCode) != "" && stringValue(p.ManualPairingCode) != "" {
		return fmt.Errorf("%w: pairingCode and manualPairingCode are mutually exclusive", ErrInvalidRequest)
	}
	return nil
}

type PairingStatusResponse struct {
	Claimed bool `json:"claimed"`
}

type ClientsListParams struct {
	EnvironmentID string            `json:"environmentId"`
	Cursor        *string           `json:"cursor,omitempty"`
	Limit         *uint32           `json:"limit,omitempty"`
	Order         *ClientsListOrder `json:"order,omitempty"`
}

func (p *ClientsListParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: remote control client list requires environmentId", ErrInvalidRequest)
	}
	if p.Limit != nil && (*p.Limit < 1 || *p.Limit > 100) {
		return fmt.Errorf("%w: remote control client list limit must be between 1 and 100", ErrInvalidRequest)
	}
	return nil
}

type ClientsListOrder string

const (
	OrderAsc  ClientsListOrder = "asc"
	OrderDesc ClientsListOrder = "desc"
)

type ClientsListResponse struct {
	Data       []Client `json:"data"`
	NextCursor *string  `json:"nextCursor"`
}

func (r *ClientsListResponse) MarshalJSON() ([]byte, error) {
	data := append([]Client(nil), r.Data...)
	if data == nil {
		data = []Client{}
	}
	return json.Marshal(struct {
		Data       []Client `json:"data"`
		NextCursor *string  `json:"nextCursor"`
	}{
		Data:       data,
		NextCursor: cloneStringPtr(r.NextCursor),
	})
}

type Client struct {
	ClientID    string  `json:"clientId"`
	DisplayName *string `json:"displayName"`
	DeviceType  *string `json:"deviceType"`
	Platform    *string `json:"platform"`
	OSVersion   *string `json:"osVersion"`
	DeviceModel *string `json:"deviceModel"`
	AppVersion  *string `json:"appVersion"`
	LastSeenAt  *int64  `json:"lastSeenAt"`
}

type ClientsRevokeParams struct {
	EnvironmentID string `json:"environmentId"`
	ClientID      string `json:"clientId"`
}

func (p *ClientsRevokeParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: remote control client revoke requires environmentId", ErrInvalidRequest)
	}
	if strings.TrimSpace(p.ClientID) == "" {
		return fmt.Errorf("%w: remote control client revoke requires clientId", ErrInvalidRequest)
	}
	return nil
}

type ClientsRevokeResponse struct{}

func ParseAppServerVersionFromUserAgent(userAgent string) (string, error) {
	_, rest, ok := strings.Cut(userAgent, "/")
	if !ok {
		return "", fmt.Errorf("%w: app-server user-agent omitted version separator", ErrInvalidRequest)
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || fields[0] == "" {
		return "", fmt.Errorf("%w: app-server user-agent omitted version", ErrInvalidRequest)
	}
	return fields[0], nil
}

type Manager struct {
	mu             sync.Mutex
	status         ConnectionStatus
	serverName     string
	installationID string
	environmentID  *string
	backend        *ManagerBackendOptions
	enrollment     *Enrollment
	pairingSeq     int
	clientByEnv    map[string]map[string]Client
	pairings       map[string]*pairing
	now            func() time.Time
}

type RemoteControlAuthLoader func(ctx context.Context) (*RemoteControlConnectionAuth, error)
type RemoteControlAuthRecovery func(ctx context.Context, previous *RemoteControlConnectionAuth) (*RemoteControlConnectionAuth, bool, error)

type ManagerBackendOptions struct {
	Target              *RemoteControlTarget
	RemoteControlURL    string
	Store               *EnrollmentStore
	CloseStoreOnClose   bool
	AuthLoader          RemoteControlAuthLoader
	AuthRecovery        RemoteControlAuthRecovery
	AuthRecoveryReset   func()
	AuthRecoveryChanged func() bool
	ServerAPIOptions    *ServerAPIOptions
	AppServerClientName *string
}

type pairing struct {
	code       string
	manualCode *string
	envID      string
	expiresAt  int64
	claimed    bool
}

func NewManager(serverName string, installationID string) *Manager {
	return &Manager{
		status:         StatusDisabled,
		serverName:     serverName,
		installationID: installationID,
		clientByEnv:    map[string]map[string]Client{},
		pairings:       map[string]*pairing{},
		now:            time.Now,
	}
}

func NewManagerWithBackend(serverName string, installationID string, backend *ManagerBackendOptions) *Manager {
	manager := NewManager(serverName, installationID)
	manager.ConfigureBackend(backend)
	return manager
}

func (m *Manager) ConfigureBackend(backend *ManagerBackendOptions) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backend = cloneManagerBackendOptions(backend)
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	backend := m.backend
	m.backend = nil
	m.mu.Unlock()
	if backend != nil && backend.CloseStoreOnClose && backend.Store != nil {
		return backend.Store.Close()
	}
	return nil
}

func (m *Manager) ResetAuthRecovery() {
	if m == nil {
		return
	}
	m.mu.Lock()
	backend := cloneManagerBackendOptions(m.backend)
	m.mu.Unlock()
	if backend != nil && backend.AuthRecoveryReset != nil {
		backend.AuthRecoveryReset()
	}
}

func (m *Manager) ConsumeAuthRecoveryChanged() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	backend := cloneManagerBackendOptions(m.backend)
	m.mu.Unlock()
	return backend != nil && backend.AuthRecoveryChanged != nil && backend.AuthRecoveryChanged()
}

func (m *Manager) ensureLocked() {
	if m.status == "" {
		m.status = StatusDisabled
	}
	if m.clientByEnv == nil {
		m.clientByEnv = map[string]map[string]Client{}
	}
	if m.pairings == nil {
		m.pairings = map[string]*pairing{}
	}
	if m.now == nil {
		m.now = time.Now
	}
}

func (m *Manager) SetClock(clock func() time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if clock == nil {
		m.now = time.Now
		return
	}
	m.now = clock
}

func (m *Manager) Enable(params *EnableParams) (*EnableResponse, *StatusChangedNotification) {
	response, notification, _ := m.EnableContext(context.Background(), params)
	return response, notification
}

func (m *Manager) EnableContext(ctx context.Context, params *EnableParams) (*EnableResponse, *StatusChangedNotification, error) {
	if m == nil {
		return nil, nil, nil
	}
	if params == nil {
		params = &EnableParams{}
	}
	if !params.Ephemeral {
		m.mu.Lock()
		backend := cloneManagerBackendOptions(m.backend)
		m.mu.Unlock()
		if backend != nil {
			return m.enableDurable(ctx, params, backend)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	previous := m.statusLocked()
	m.status = StatusConnected
	if m.environmentID == nil {
		envID := "default"
		m.environmentID = &envID
	}
	notification := m.statusLocked()
	response := EnableResponseFromNotification(notification)
	if statusChangedNotificationEqual(previous, notification) {
		return response, nil, nil
	}
	return response, notification, nil
}

func (m *Manager) Disable(params *DisableParams) (*DisableResponse, *StatusChangedNotification) {
	response, notification, _ := m.DisableContext(context.Background(), params)
	return response, notification
}

func (m *Manager) DisableContext(ctx context.Context, params *DisableParams) (*DisableResponse, *StatusChangedNotification, error) {
	if m == nil {
		return nil, nil, nil
	}
	if params == nil {
		params = &DisableParams{}
	}
	if !params.Ephemeral {
		m.mu.Lock()
		backend := cloneManagerBackendOptions(m.backend)
		m.mu.Unlock()
		if backend != nil {
			return m.disableDurable(ctx, params, backend)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	previous := m.statusLocked()
	m.status = StatusDisabled
	m.environmentID = nil
	notification := m.statusLocked()
	response := DisableResponseFromNotification(notification)
	if statusChangedNotificationEqual(previous, notification) {
		return response, nil, nil
	}
	return response, notification, nil
}

func (m *Manager) Status() *StatusReadResponse {
	if m == nil {
		return &StatusReadResponse{Status: StatusDisabled}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	notification := m.statusLocked()
	return &StatusReadResponse{
		Status:         notification.Status,
		ServerName:     notification.ServerName,
		InstallationID: notification.InstallationID,
		EnvironmentID:  cloneStringPtr(notification.EnvironmentID),
	}
}

func (m *Manager) StatusChanged() *StatusChangedNotification {
	if m == nil {
		return &StatusChangedNotification{Status: StatusDisabled}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	return m.statusLocked()
}

func (m *Manager) PublishConnectionStatus(status ConnectionStatus) *StatusChangedNotification {
	if m == nil || status == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	previous := m.statusLocked()
	m.status = status
	if status == StatusDisabled {
		m.environmentID = nil
	}
	notification := m.statusLocked()
	if statusChangedNotificationEqual(previous, notification) {
		return nil
	}
	return notification
}

func (m *Manager) PublishEnvironmentID(environmentID *string) *StatusChangedNotification {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if m.status == StatusDisabled {
		return nil
	}
	previous := m.statusLocked()
	m.environmentID = cloneStringPtr(environmentID)
	notification := m.statusLocked()
	if statusChangedNotificationEqual(previous, notification) {
		return nil
	}
	return notification
}

func (m *Manager) StartPairing(params *PairingStartParams) (*PairingStartResponse, error) {
	return m.StartPairingContext(context.Background(), params)
}

func (m *Manager) StartPairingContext(ctx context.Context, params *PairingStartParams) (*PairingStartResponse, error) {
	if params == nil {
		params = &PairingStartParams{}
	}
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	backend := cloneManagerBackendOptions(m.backend)
	m.mu.Unlock()
	if backend != nil {
		return m.startPairingDurable(ctx, params, backend)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if m.environmentID == nil || strings.TrimSpace(*m.environmentID) == "" {
		envID := "default"
		m.environmentID = &envID
	}
	m.pairingSeq++
	code := fmt.Sprintf("codex-%06d", m.pairingSeq)
	expiresAt := m.now().Add(10 * time.Minute).Unix()
	var manual *string
	if params.ManualCode {
		value := fmt.Sprintf("%06d", m.pairingSeq)
		manual = &value
	}
	item := &pairing{
		code:       code,
		manualCode: manual,
		envID:      *m.environmentID,
		expiresAt:  expiresAt,
	}
	m.pairings[code] = item
	if manual != nil {
		m.pairings[*manual] = item
	}
	return &PairingStartResponse{
		PairingCode:       code,
		ManualPairingCode: cloneStringPtr(manual),
		EnvironmentID:     item.envID,
		ExpiresAt:         expiresAt,
	}, nil
}

func (m *Manager) PairingStatus(params *PairingStatusParams) (*PairingStatusResponse, error) {
	return m.PairingStatusContext(context.Background(), params)
}

func (m *Manager) PairingStatusContext(ctx context.Context, params *PairingStatusParams) (*PairingStatusResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	backend := cloneManagerBackendOptions(m.backend)
	m.mu.Unlock()
	if backend != nil {
		return m.pairingStatusDurable(ctx, params, backend)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	item := m.lookupPairingLocked(params)
	return &PairingStatusResponse{Claimed: item != nil && item.claimed}, nil
}

func (m *Manager) ClaimPairing(params *PairingStatusParams) (bool, error) {
	if err := params.Validate(); err != nil {
		return false, err
	}
	if m == nil {
		return false, fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	item := m.lookupPairingLocked(params)
	if item == nil {
		return false, nil
	}
	item.claimed = true
	envID := item.envID
	m.environmentID = &envID
	m.status = StatusConnected
	return true, nil
}

func (m *Manager) UpsertClient(environmentID string, client Client) error {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidRequest)
	}
	client.ClientID = strings.TrimSpace(client.ClientID)
	if client.ClientID == "" {
		return fmt.Errorf("%w: clientId is required", ErrInvalidRequest)
	}
	if m == nil {
		return fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	byClient := m.clientByEnv[environmentID]
	if byClient == nil {
		byClient = map[string]Client{}
		m.clientByEnv[environmentID] = byClient
	}
	byClient[client.ClientID] = cloneClient(client)
	return nil
}

func (m *Manager) ListClients(params *ClientsListParams) (*ClientsListResponse, error) {
	return m.ListClientsContext(context.Background(), params)
}

func (m *Manager) ListClientsContext(ctx context.Context, params *ClientsListParams) (*ClientsListResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if m != nil {
		m.mu.Lock()
		backend := cloneManagerBackendOptions(m.backend)
		m.mu.Unlock()
		if backend != nil {
			return listRemoteControlClients(ctx, backend, params)
		}
	}
	start, err := parseCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	limit := 50
	if params.Limit != nil && *params.Limit > 0 {
		limit = int(*params.Limit)
	}
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	clients := make([]Client, 0, len(m.clientByEnv[params.EnvironmentID]))
	for _, client := range m.clientByEnv[params.EnvironmentID] {
		clients = append(clients, cloneClient(client))
	}
	sort.SliceStable(clients, func(i int, j int) bool {
		if params.Order != nil && *params.Order == OrderDesc {
			return clients[i].ClientID > clients[j].ClientID
		}
		return clients[i].ClientID < clients[j].ClientID
	})
	page, next := paginate(clients, start, limit)
	return &ClientsListResponse{Data: page, NextCursor: stringPtrIfNotEmpty(next)}, nil
}

func (m *Manager) RevokeClient(params *ClientsRevokeParams) (*ClientsRevokeResponse, error) {
	return m.RevokeClientContext(context.Background(), params)
}

func (m *Manager) RevokeClientContext(ctx context.Context, params *ClientsRevokeParams) (*ClientsRevokeResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if m != nil {
		m.mu.Lock()
		backend := cloneManagerBackendOptions(m.backend)
		m.mu.Unlock()
		if backend != nil {
			return revokeRemoteControlClient(ctx, backend, params)
		}
	}
	if m == nil {
		return nil, fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	if byClient := m.clientByEnv[params.EnvironmentID]; byClient != nil {
		delete(byClient, params.ClientID)
	}
	return &ClientsRevokeResponse{}, nil
}

func (m *Manager) statusLocked() *StatusChangedNotification {
	return &StatusChangedNotification{
		Status:         m.status,
		ServerName:     m.serverName,
		InstallationID: m.installationID,
		EnvironmentID:  cloneStringPtr(m.environmentID),
	}
}

func (m *Manager) lookupPairingLocked(params *PairingStatusParams) *pairing {
	for _, key := range []string{stringValue(params.PairingCode), stringValue(params.ManualPairingCode)} {
		if key == "" {
			continue
		}
		if item := m.pairings[key]; item != nil {
			return item
		}
	}
	return nil
}

func EnableResponseFromNotification(notification *StatusChangedNotification) *EnableResponse {
	if notification == nil {
		return nil
	}
	return &EnableResponse{
		Status:         notification.Status,
		ServerName:     notification.ServerName,
		InstallationID: notification.InstallationID,
		EnvironmentID:  cloneStringPtr(notification.EnvironmentID),
	}
}

func DisableResponseFromNotification(notification *StatusChangedNotification) *DisableResponse {
	if notification == nil {
		return nil
	}
	return &DisableResponse{
		Status:         notification.Status,
		ServerName:     notification.ServerName,
		InstallationID: notification.InstallationID,
		EnvironmentID:  cloneStringPtr(notification.EnvironmentID),
	}
}

func parseCursor(cursor *string) (int, error) {
	if cursor == nil || *cursor == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(*cursor)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%w: invalid cursor", ErrInvalidRequest)
	}
	return value, nil
}

func paginate(clients []Client, start int, limit int) ([]Client, string) {
	if start >= len(clients) {
		return []Client{}, ""
	}
	if limit <= 0 {
		limit = 50
	}
	end := start + limit
	if end > len(clients) {
		end = len(clients)
	}
	next := ""
	if end < len(clients) {
		next = strconv.Itoa(end)
	}
	return append([]Client(nil), clients[start:end]...), next
}

func cloneClient(client Client) Client {
	client.DisplayName = cloneStringPtr(client.DisplayName)
	client.DeviceType = cloneStringPtr(client.DeviceType)
	client.Platform = cloneStringPtr(client.Platform)
	client.OSVersion = cloneStringPtr(client.OSVersion)
	client.DeviceModel = cloneStringPtr(client.DeviceModel)
	client.AppVersion = cloneStringPtr(client.AppVersion)
	if client.LastSeenAt != nil {
		value := *client.LastSeenAt
		client.LastSeenAt = &value
	}
	return client
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func stringPtrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func statusChangedNotificationEqual(left *StatusChangedNotification, right *StatusChangedNotification) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Status == right.Status &&
		left.ServerName == right.ServerName &&
		left.InstallationID == right.InstallationID &&
		stringPtrEqual(left.EnvironmentID, right.EnvironmentID)
}

func stringPtrEqual(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
