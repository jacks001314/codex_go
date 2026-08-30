package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	execserverclient "codex_go/execserver"

	"github.com/coder/websocket"
)

var ErrInvalidEnvironmentRequest = errors.New("invalid environment request")

// ErrEnvironmentProvisioningModeConflict reports that an environment is already
// registered with the opposite provisioning mode (ordinary vs. provisioned).
// Mirrors Rust exec-server ProvisioningModeConflict.
var ErrEnvironmentProvisioningModeConflict = errors.New("environment provisioning mode conflict")

const defaultEnvironmentConnectTimeout = 10 * time.Second

// maxSelectedCapabilityRoots bounds the capability roots accepted from
// environment ready information. Mirrors Rust MAX_SELECTED_CAPABILITY_ROOTS.
const maxSelectedCapabilityRoots = 256

// ProvisioningStatusKind tracks a provisioned (deferred) Noise environment's
// provisioning state. Mirrors Rust exec-server provisioning states.
type ProvisioningStatusKind string

const (
	ProvisioningPending ProvisioningStatusKind = "pending"
	ProvisioningReady   ProvisioningStatusKind = "ready"
	ProvisioningFailed  ProvisioningStatusKind = "failed"
)

// EnvironmentReadyInfo is the capability information supplied when a
// provisioned environment becomes ready. Mirrors Rust EnvironmentReadyInfo.
type EnvironmentReadyInfo struct {
	SelectedCapabilityRoots []SelectedCapabilityRoot `json:"selectedCapabilityRoots"`
}

// ProvisioningState tracks a provisioned Noise environment from Pending through
// Ready or Failed. The same instance is preserved across materialization and
// status reports; terminal transitions are idempotent and contradictory
// transitions are rejected. A nil ProvisioningState on a record means the
// environment is ordinary and connects eagerly.
type ProvisioningState struct {
	mu        sync.Mutex
	status    ProvisioningStatusKind
	readyInfo *EnvironmentReadyInfo
	failure   string
}

func newProvisioningState() *ProvisioningState {
	return &ProvisioningState{status: ProvisioningPending}
}

// Current returns the provisioning status, the most recently reported ready
// info (nil unless Ready), and the first failure message (empty unless Failed).
func (s *ProvisioningState) Current() (ProvisioningStatusKind, *EnvironmentReadyInfo, string) {
	if s == nil {
		return ProvisioningPending, nil, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, cloneEnvironmentReadyInfo(s.readyInfo), s.failure
}

// applyReady transitions Pending->Ready (validating ready info) or refreshes
// ready info on an already Ready environment. A Ready report after Failed is a
// contradictory transition; invalid ready info fails a Pending environment.
func (s *ProvisioningState) applyReady(environmentID string, readyInfo *EnvironmentReadyInfo) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.status {
	case ProvisioningFailed:
		return fmt.Errorf("environment `%s` provisioning already failed: %s", environmentID, s.failure)
	case ProvisioningReady:
		if err := validateEnvironmentReadyInfo(environmentID, readyInfo); err != nil {
			return err
		}
		s.readyInfo = cloneEnvironmentReadyInfo(readyInfo)
		return nil
	default: // Pending
		if err := validateEnvironmentReadyInfo(environmentID, readyInfo); err != nil {
			s.status = ProvisioningFailed
			s.failure = err.Error()
			return err
		}
		s.readyInfo = cloneEnvironmentReadyInfo(readyInfo)
		s.status = ProvisioningReady
		return nil
	}
}

// applyFailure transitions Pending->Failed, keeping the first error. Repeating
// a failure on an already Failed environment is idempotent; a failure after
// Ready is a contradictory transition.
func (s *ProvisioningState) applyFailure(environmentID, failure string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.status {
	case ProvisioningReady:
		return fmt.Errorf("environment `%s` is already ready, but a later provisioning report failed: %s", environmentID, failure)
	case ProvisioningFailed:
		return nil
	default: // Pending
		s.status = ProvisioningFailed
		s.failure = failure
		return nil
	}
}

// validateEnvironmentReadyInfo enforces the ready-info contract shared by
// readiness publication, deferred completion, and provisioning reports.
func validateEnvironmentReadyInfo(environmentID string, readyInfo *EnvironmentReadyInfo) error {
	if readyInfo == nil {
		return fmt.Errorf("environment ready info is nil")
	}
	if len(readyInfo.SelectedCapabilityRoots) > maxSelectedCapabilityRoots {
		return fmt.Errorf("environment ready info contains more than %d selected capability roots", maxSelectedCapabilityRoots)
	}
	seen := make(map[string]struct{}, len(readyInfo.SelectedCapabilityRoots))
	for _, root := range readyInfo.SelectedCapabilityRoots {
		if strings.TrimSpace(root.ID) == "" || root.Location.EnvironmentID != environmentID {
			return fmt.Errorf("selected capability roots must have unique non-empty IDs and belong to environment `%s`", environmentID)
		}
		if _, dup := seen[root.ID]; dup {
			return fmt.Errorf("selected capability roots must have unique non-empty IDs and belong to environment `%s`", environmentID)
		}
		seen[root.ID] = struct{}{}
	}
	return nil
}

type EnvironmentAddParams struct {
	EnvironmentID    string  `json:"environmentId"`
	ExecServerURL    string  `json:"execServerUrl"`
	ConnectTimeoutMS *uint64 `json:"connectTimeoutMs,omitempty"`
}

func (m *EnvironmentManager) SetHTTPClient(httpClient *http.Client) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpClient = httpClient
	for id, record := range m.records {
		record.HTTPClient = httpClient
		m.records[id] = record
	}
}

func (p *EnvironmentAddParams) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: params are nil", ErrInvalidEnvironmentRequest)
	}
	if strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	parsed, err := url.Parse(strings.TrimSpace(p.ExecServerURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%w: execServerUrl must be absolute", ErrInvalidEnvironmentRequest)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return fmt.Errorf("%w: execServerUrl must use ws or wss", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentAddResponse struct{}

type EnvironmentInfoParams struct {
	EnvironmentID string `json:"environmentId"`
}

func (p *EnvironmentInfoParams) Validate() error {
	if p == nil || strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentInfoResponse struct {
	Shell        EnvironmentShellInfo                     `json:"shell"`
	CWD          *string                                  `json:"cwd"`
	PlatformOS   string                                   `json:"platformOs,omitempty"`
	UserHomeDir  string                                   `json:"userHomeDir,omitempty"`
	Capabilities execserverclient.EnvironmentCapabilities `json:"capabilities"`
}

type EnvironmentStatusParams struct {
	EnvironmentID string `json:"environmentId"`
}

func (p *EnvironmentStatusParams) Validate() error {
	if p == nil || strings.TrimSpace(p.EnvironmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentStatusKind string

const (
	EnvironmentStatusReady        EnvironmentStatusKind = "ready"
	EnvironmentStatusPending      EnvironmentStatusKind = "pending"
	EnvironmentStatusDisconnected EnvironmentStatusKind = "disconnected"
	EnvironmentStatusUnknown      EnvironmentStatusKind = "unknown"
)

type EnvironmentStatusResponse struct {
	Status EnvironmentStatusKind `json:"status"`
	Error  *string               `json:"error,omitempty"`
}

type EnvironmentShellInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *EnvironmentShellInfo) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: shell is nil", ErrInvalidEnvironmentRequest)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: shell name is required", ErrInvalidEnvironmentRequest)
	}
	if strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("%w: shell path is required", ErrInvalidEnvironmentRequest)
	}
	return nil
}

type EnvironmentRecord struct {
	EnvironmentID    string
	ExecServerURL    string
	NoiseProvider    execserverclient.NoiseRendezvousConnectProvider
	ConnectTimeoutMS *uint64
	Shell            EnvironmentShellInfo
	CWD              *string
	InfoOverride     bool
	HTTPClient       *http.Client
	// Provisioning is non-nil for provisioned (deferred) Noise environments and
	// nil for ordinary environments, which connect eagerly.
	Provisioning *ProvisioningState
}

type EnvironmentManager struct {
	mu           sync.Mutex
	defaultShell EnvironmentShellInfo
	defaultCWD   *string
	records      map[string]EnvironmentRecord
	httpClient   *http.Client
}

func NewEnvironmentManager(defaultShell EnvironmentShellInfo, defaultCWD string) *EnvironmentManager {
	return NewEnvironmentManagerWithHTTPClient(defaultShell, defaultCWD, nil)
}

func NewEnvironmentManagerWithHTTPClient(defaultShell EnvironmentShellInfo, defaultCWD string, httpClient *http.Client) *EnvironmentManager {
	return &EnvironmentManager{
		defaultShell: defaultShell,
		defaultCWD:   pathURI(defaultCWD),
		records:      map[string]EnvironmentRecord{},
		httpClient:   httpClient,
	}
}

func (m *EnvironmentManager) Add(params *EnvironmentAddParams) (*EnvironmentAddResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := EnvironmentRecord{
		EnvironmentID:    strings.TrimSpace(params.EnvironmentID),
		ExecServerURL:    strings.TrimSpace(params.ExecServerURL),
		ConnectTimeoutMS: cloneUint64Ptr(params.ConnectTimeoutMS),
		Shell:            m.defaultShell,
		CWD:              cloneString(m.defaultCWD),
		HTTPClient:       m.httpClient,
	}
	m.records[record.EnvironmentID] = record
	return &EnvironmentAddResponse{}, nil
}

func (m *EnvironmentManager) AddNoise(environmentID string, provider execserverclient.NoiseRendezvousConnectProvider) error {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if provider == nil {
		return fmt.Errorf("%w: Noise rendezvous provider is required", ErrInvalidEnvironmentRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[environmentID] = EnvironmentRecord{
		EnvironmentID: environmentID,
		NoiseProvider: provider,
		Shell:         m.defaultShell,
		CWD:           cloneString(m.defaultCWD),
		HTTPClient:    m.httpClient,
	}
	return nil
}

// DeferredEnvironmentRegistration completes a pending provisioned environment
// registration. Completing is terminal; abandoning records a failure without
// connecting, mirroring Rust's Drop behavior for the registration handle.
type DeferredEnvironmentRegistration struct {
	environmentID string
	state         *ProvisioningState
	completed     bool
}

// EnvironmentID returns the environment this registration completes.
func (r *DeferredEnvironmentRegistration) EnvironmentID() string {
	if r == nil {
		return ""
	}
	return r.environmentID
}

// CompleteReady publishes the ready information and transitions the pending
// environment to Ready. Invalid ready information fails the environment and
// returns an error; completing twice is rejected as inactive.
func (r *DeferredEnvironmentRegistration) CompleteReady(readyInfo EnvironmentReadyInfo) error {
	if r == nil || r.state == nil || r.completed {
		return errors.New("deferred environment registration is inactive")
	}
	r.completed = true
	return r.state.applyReady(r.environmentID, &readyInfo)
}

// CompleteFailed transitions the pending environment to Failed with a terminal
// error message, keeping the first error on repeated reports.
func (r *DeferredEnvironmentRegistration) CompleteFailed(message string) error {
	if r == nil || r.state == nil || r.completed {
		return errors.New("deferred environment registration is inactive")
	}
	r.completed = true
	return r.state.applyFailure(r.environmentID, message)
}

// Abandon records the registration as ended before completion, failing the
// pending environment without ever connecting. Safe to call after completion.
func (r *DeferredEnvironmentRegistration) Abandon() {
	if r == nil || r.state == nil || r.completed {
		return
	}
	r.completed = true
	_ = r.state.applyFailure(r.environmentID, "environment registration ended before completion")
}

// RegisterDeferredNoiseEnvironment adds or replaces a Noise environment that
// becomes ready later. The returned registration completes provisioning:
// Ready publishes capability roots, and failure (or abandonment) marks the
// environment Failed before any connection is attempted.
func (m *EnvironmentManager) RegisterDeferredNoiseEnvironment(environmentID string, provider execserverclient.NoiseRendezvousConnectProvider) (*DeferredEnvironmentRegistration, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: Noise rendezvous provider is required", ErrInvalidEnvironmentRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := newProvisioningState()
	m.records[environmentID] = EnvironmentRecord{
		EnvironmentID: environmentID,
		NoiseProvider: provider,
		Shell:         m.defaultShell,
		CWD:           cloneString(m.defaultCWD),
		HTTPClient:    m.httpClient,
		Provisioning:  state,
	}
	return &DeferredEnvironmentRegistration{environmentID: environmentID, state: state}, nil
}

// MaterializePendingNoiseEnvironment returns the stable environment for an ID,
// creating it as Pending when absent. A provisioned environment keeps the same
// record from Pending through Ready or Failed, so the passed provider is only
// used when the environment does not exist yet. Conflicts with ordinary
// environments are rejected.
func (m *EnvironmentManager) MaterializePendingNoiseEnvironment(environmentID string, provider execserverclient.NoiseRendezvousConnectProvider) (*EnvironmentRecord, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if provider == nil {
		return nil, fmt.Errorf("%w: Noise rendezvous provider is required", ErrInvalidEnvironmentRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if record, ok := m.records[environmentID]; ok {
		if record.Provisioning == nil {
			return nil, fmt.Errorf("%w: environment `%s` is already registered with a different provisioning mode", ErrEnvironmentProvisioningModeConflict, environmentID)
		}
		cloned := cloneEnvironmentRecord(record)
		return &cloned, nil
	}
	record := EnvironmentRecord{
		EnvironmentID: environmentID,
		NoiseProvider: provider,
		Shell:         m.defaultShell,
		CWD:           cloneString(m.defaultCWD),
		HTTPClient:    m.httpClient,
		Provisioning:  newProvisioningState(),
	}
	m.records[environmentID] = record
	cloned := cloneEnvironmentRecord(record)
	return &cloned, nil
}

// ReportProvisioningStatus records a Ready or Failed provisioning result for an
// environment. Ordinary environments are ignored. A provisioned environment
// keeps the same record from Pending through Ready or Failed, and is created if
// the report arrives first. Ready updates capability roots; Failed keeps the
// first error. Repeating the same result is allowed, but changing between Ready
// and Failed is rejected. Invalid Ready information fails an existing Pending
// environment but does not create a missing environment.
func (m *EnvironmentManager) ReportProvisioningStatus(environmentID string, readyInfo *EnvironmentReadyInfo, failure *string, providerIfMissing execserverclient.NoiseRendezvousConnectProvider) (*EnvironmentRecord, error) {
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if providerIfMissing == nil {
		return nil, fmt.Errorf("%w: Noise rendezvous provider is required", ErrInvalidEnvironmentRequest)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if record, ok := m.records[environmentID]; ok {
		if record.Provisioning == nil {
			// Ordinary environments have no provisioning state and are ignored.
			return nil, nil
		}
		if failure != nil {
			cloned := cloneEnvironmentRecord(record)
			return &cloned, record.Provisioning.applyFailure(environmentID, *failure)
		}
		cloned := cloneEnvironmentRecord(record)
		return &cloned, record.Provisioning.applyReady(environmentID, readyInfo)
	}
	state := newProvisioningState()
	if failure != nil {
		if err := state.applyFailure(environmentID, *failure); err != nil {
			return nil, err
		}
	} else {
		// Invalid ready information must not create a missing environment.
		if err := validateEnvironmentReadyInfo(environmentID, readyInfo); err != nil {
			return nil, err
		}
		if err := state.applyReady(environmentID, readyInfo); err != nil {
			return nil, err
		}
	}
	record := EnvironmentRecord{
		EnvironmentID: environmentID,
		NoiseProvider: providerIfMissing,
		Shell:         m.defaultShell,
		CWD:           cloneString(m.defaultCWD),
		HTTPClient:    m.httpClient,
		Provisioning:  state,
	}
	m.records[environmentID] = record
	cloned := cloneEnvironmentRecord(record)
	return &cloned, nil
}

// SelectedCapabilityRoots returns the capability roots most recently reported
// for a provisioned environment, or nil for ordinary or unknown environments.
func (m *EnvironmentManager) SelectedCapabilityRoots(environmentID string) []SelectedCapabilityRoot {
	if m == nil {
		return nil
	}
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil
	}
	m.mu.Lock()
	record, ok := m.records[environmentID]
	m.mu.Unlock()
	if !ok || record.Provisioning == nil {
		return nil
	}
	_, readyInfo, _ := record.Provisioning.Current()
	if readyInfo == nil {
		return nil
	}
	return cloneSelectedCapabilityRoots(readyInfo.SelectedCapabilityRoots)
}

// SelectedCapabilityRootsStatus is a passive view of selected capability roots
// and unavailable environments. Mirrors Rust's
// SelectedCapabilityRootsStatus (codex-rs/exec-server/src/resolved_capability.rs).
type SelectedCapabilityRootsStatus struct {
	// ReadyRoots are selected roots whose environments are ready.
	ReadyRoots []SelectedCapabilityRoot `json:"readyRoots"`
	// Warnings describe missing environments and terminal connection failures.
	Warnings []string `json:"warnings"`
}

// InspectSelectedCapabilityRoots merges the persisted thread roots with the
// ready attachment roots installed by environment readiness reports
// (Rust ThreadEnvironments::inspect_selected_capability_roots, #38067).
// Thread roots stay first; duplicate root IDs are dropped keeping the first
// occurrence. Roots whose environments are not ready are omitted, and missing
// or failed environments produce warnings.
func (m *EnvironmentManager) InspectSelectedCapabilityRoots(threadRoots []SelectedCapabilityRoot) SelectedCapabilityRootsStatus {
	status := SelectedCapabilityRootsStatus{}
	if m == nil {
		status.ReadyRoots = cloneSelectedCapabilityRoots(threadRoots)
		return status
	}
	m.mu.Lock()
	records := make(map[string]EnvironmentRecord, len(m.records))
	for id, record := range m.records {
		records[id] = cloneEnvironmentRecord(record)
	}
	m.mu.Unlock()

	merged := combineSelectedCapabilityRoots(threadRoots, m.readyAttachmentRoots(records))
	seen := make(map[string]struct{}, len(merged))
	for _, root := range merged {
		if _, dup := seen[root.ID]; dup {
			continue
		}
		seen[root.ID] = struct{}{}
		status.ReadyRoots = append(status.ReadyRoots, root)
	}
	status.ReadyRoots = m.filterReadyRoots(status.ReadyRoots, records, &status.Warnings)
	return status
}

// combineSelectedCapabilityRoots mirrors Rust combine_selected_capability_roots
// (#39746): a matching live attachment root (same root id and environment id)
// refreshes the persisted thread-owned location, while persisted roots remain
// when the executor reports none. Attachment roots are then appended in
// selection order; duplicates are dropped by the caller.
func combineSelectedCapabilityRoots(threadRoots []SelectedCapabilityRoot, attachmentRoots []SelectedCapabilityRoot) []SelectedCapabilityRoot {
	combined := make([]SelectedCapabilityRoot, 0, len(threadRoots)+len(attachmentRoots))
	for _, threadRoot := range threadRoots {
		replacement := threadRoot
		if threadRoot.Location.Type == CapabilityRootLocationEnvironment {
			for _, attachmentRoot := range attachmentRoots {
				if attachmentRoot.ID != threadRoot.ID ||
					attachmentRoot.Location.Type != CapabilityRootLocationEnvironment ||
					attachmentRoot.Location.EnvironmentID != threadRoot.Location.EnvironmentID {
					continue
				}
				replacement = attachmentRoot
				break
			}
		}
		combined = append(combined, replacement)
	}
	combined = append(combined, attachmentRoots...)
	return combined
}

// readyAttachmentRoots returns the selected capability roots installed by
// ready provisioning reports on all managed environments.
func (m *EnvironmentManager) readyAttachmentRoots(records map[string]EnvironmentRecord) []SelectedCapabilityRoot {
	var roots []SelectedCapabilityRoot
	for _, record := range records {
		if record.Provisioning == nil {
			continue
		}
		_, readyInfo, _ := record.Provisioning.Current()
		if readyInfo == nil {
			continue
		}
		roots = append(roots, cloneSelectedCapabilityRoots(readyInfo.SelectedCapabilityRoots)...)
	}
	return roots
}

// filterReadyRoots keeps roots whose environment is ready, emitting warnings
// for missing or failed environments. Starting or recovering environments are
// silently omitted.
func (m *EnvironmentManager) filterReadyRoots(roots []SelectedCapabilityRoot, records map[string]EnvironmentRecord, warnings *[]string) []SelectedCapabilityRoot {
	readiness := make(map[string]bool, len(roots))
	emitted := make(map[string]bool, len(roots))
	out := make([]SelectedCapabilityRoot, 0, len(roots))
	for _, root := range roots {
		if root.Location.Type != CapabilityRootLocationEnvironment {
			out = append(out, root)
			continue
		}
		environmentID := root.Location.EnvironmentID
		// The primary local environment is always considered ready for
		// capability-root inspection, matching Rust's always-present local
		// environment.
		if environmentID == "" || environmentID == "local" {
			out = append(out, root)
			continue
		}
		ready, known := readiness[environmentID]
		if !known {
			record, exists := records[environmentID]
			switch {
			case !exists:
				ready = false
				*warnings = append(*warnings, fmt.Sprintf("selected capability root `%s` references unavailable environment `%s`", root.ID, environmentID))
				emitted[environmentID] = true
			case record.Provisioning == nil:
				// Ordinary environments connect eagerly; treat them as ready
				// only when info was overridden by a host report.
				ready = record.InfoOverride
			default:
				status, _, failure := record.Provisioning.Current()
				switch status {
				case ProvisioningReady:
					ready = true
				case ProvisioningFailed:
					ready = false
					*warnings = append(*warnings, fmt.Sprintf("selected capability environment `%s` is unavailable: %s", environmentID, failure))
					emitted[environmentID] = true
				default:
					ready = false
				}
			}
			readiness[environmentID] = ready
		}
		if !ready && !emitted[environmentID] {
			// Starting/recovering environments are omitted without a warning.
			continue
		}
		if ready {
			out = append(out, root)
		}
	}
	return out
}

func (m *EnvironmentManager) SetInfo(environmentID string, shell EnvironmentShellInfo, cwd string) error {
	if strings.TrimSpace(environmentID) == "" {
		return fmt.Errorf("%w: environmentId is required", ErrInvalidEnvironmentRequest)
	}
	if err := shell.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.records[environmentID]
	if record.EnvironmentID == "" {
		record.EnvironmentID = environmentID
	}
	record.Shell = shell
	record.CWD = pathURI(cwd)
	record.InfoOverride = true
	m.records[environmentID] = record
	return nil
}

func (m *EnvironmentManager) Info(params *EnvironmentInfoParams) (*EnvironmentInfoResponse, error) {
	return m.InfoContext(context.Background(), params)
}

func (m *EnvironmentManager) InfoContext(ctx context.Context, params *EnvironmentInfoParams) (*EnvironmentInfoResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	record, ok := m.records[params.EnvironmentID]
	defaultShell := m.defaultShell
	defaultCWD := cloneString(m.defaultCWD)
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: unknown environment id `%s`", ErrInvalidEnvironmentRequest, params.EnvironmentID)
	}
	if record.Provisioning != nil {
		status, _, failure := record.Provisioning.Current()
		switch status {
		case ProvisioningPending:
			return nil, fmt.Errorf("failed to get info for environment `%s`: environment is still provisioning", params.EnvironmentID)
		case ProvisioningFailed:
			return nil, fmt.Errorf("failed to get info for environment `%s`: environment provisioning failed: %s", params.EnvironmentID, failure)
		case ProvisioningReady:
			// Provisioning succeeded; fall through to the normal connection path.
		}
	}
	if !record.InfoOverride {
		info, err := fetchRemoteEnvironmentInfo(ctx, &record)
		if err != nil {
			return nil, fmt.Errorf("failed to get info for environment `%s`: %w", params.EnvironmentID, err)
		}
		return info, nil
	}
	shell := record.Shell
	if strings.TrimSpace(shell.Name) == "" {
		shell = defaultShell
	}
	cwd := record.CWD
	if cwd == nil {
		cwd = defaultCWD
	}
	userHomeDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		userHomeDir = home
	}
	return &EnvironmentInfoResponse{Shell: shell, CWD: cloneString(cwd), PlatformOS: runtime.GOOS, UserHomeDir: userHomeDir}, nil
}

func (m *EnvironmentManager) Status(params *EnvironmentStatusParams) (*EnvironmentStatusResponse, error) {
	return m.StatusContext(context.Background(), params)
}

func (m *EnvironmentManager) StatusContext(ctx context.Context, params *EnvironmentStatusParams) (*EnvironmentStatusResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	record, ok := m.records[params.EnvironmentID]
	m.mu.Unlock()
	if !ok {
		return &EnvironmentStatusResponse{
			Status: EnvironmentStatusUnknown,
			Error:  environmentStringPtr(fmt.Sprintf("unknown environment id `%s`", params.EnvironmentID)),
		}, nil
	}
	if record.Provisioning != nil {
		status, _, failure := record.Provisioning.Current()
		switch status {
		case ProvisioningPending:
			// Delay connection attempts until provisioning completes.
			return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
		case ProvisioningFailed:
			return &EnvironmentStatusResponse{
				Status: EnvironmentStatusDisconnected,
				Error:  environmentStringPtr(failure),
			}, nil
		case ProvisioningReady:
			// Provisioning succeeded; attempt the connection below.
		}
	}
	if record.InfoOverride {
		return &EnvironmentStatusResponse{Status: EnvironmentStatusReady}, nil
	}
	if strings.TrimSpace(record.ExecServerURL) == "" && record.NoiseProvider == nil {
		return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
	}
	status, err := fetchRemoteEnvironmentStatus(ctx, &record)
	if err != nil {
		return &EnvironmentStatusResponse{
			Status: EnvironmentStatusDisconnected,
			Error:  environmentStringPtr(err.Error()),
		}, nil
	}
	if status == nil || status.Status == "" {
		return &EnvironmentStatusResponse{Status: EnvironmentStatusReady}, nil
	}
	return status, nil
}

func (m *EnvironmentManager) Remove(environmentID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.records[environmentID]; !ok {
		return false
	}
	delete(m.records, environmentID)
	return true
}

func (m *EnvironmentManager) List() []EnvironmentRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EnvironmentRecord, 0, len(m.records))
	for _, record := range m.records {
		out = append(out, cloneEnvironmentRecord(record))
	}
	for i := 1; i < len(out); i++ {
		current := out[i]
		j := i - 1
		for j >= 0 && out[j].EnvironmentID > current.EnvironmentID {
			out[j+1] = out[j]
			j--
		}
		out[j+1] = current
	}
	return out
}

func (m *EnvironmentManager) Record(environmentID string) (*EnvironmentRecord, bool) {
	if m == nil {
		return nil, false
	}
	environmentID = strings.TrimSpace(environmentID)
	if environmentID == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.records[environmentID]
	if !ok {
		return nil, false
	}
	cloned := cloneEnvironmentRecord(record)
	return &cloned, true
}

func pathURI(path string) *string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "file://") {
		return &path
	}
	cleaned, err := filepath.Abs(path)
	if err != nil {
		cleaned = filepath.Clean(path)
	}
	cleaned = filepath.ToSlash(cleaned)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	value := "file://" + cleaned
	return &value
}

func cloneUint64Ptr(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneEnvironmentRecord(record EnvironmentRecord) EnvironmentRecord {
	record.ConnectTimeoutMS = cloneUint64Ptr(record.ConnectTimeoutMS)
	record.CWD = cloneString(record.CWD)
	return record
}

func cloneEnvironmentReadyInfo(info *EnvironmentReadyInfo) *EnvironmentReadyInfo {
	if info == nil {
		return nil
	}
	clone := *info
	clone.SelectedCapabilityRoots = cloneSelectedCapabilityRoots(info.SelectedCapabilityRoots)
	return &clone
}

func cloneSelectedCapabilityRoots(roots []SelectedCapabilityRoot) []SelectedCapabilityRoot {
	if roots == nil {
		return nil
	}
	out := make([]SelectedCapabilityRoot, len(roots))
	copy(out, roots)
	return out
}

func environmentStringPtr(value string) *string {
	return &value
}

type execServerJSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type execServerJSONRPCResponse struct {
	ID     RequestID        `json:"id"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *ResponseError   `json:"error,omitempty"`
	Method string           `json:"method,omitempty"`
	Params *json.RawMessage `json:"params,omitempty"`
}

func fetchRemoteEnvironmentInfo(ctx context.Context, record *EnvironmentRecord) (*EnvironmentInfoResponse, error) {
	if record == nil {
		return nil, errors.New("environment record is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	if record.NoiseProvider != nil {
		client, err := execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, execserverclient.DialClientOptions{ClientName: "codex-go", HTTPClient: record.HTTPClient})
		if err != nil {
			return nil, err
		}
		defer client.Close()
		info, err := client.EnvironmentInfo(ctx)
		if err != nil {
			return nil, err
		}
		response := &EnvironmentInfoResponse{
			Shell:        EnvironmentShellInfo{Name: info.Shell.Name, Path: info.Shell.Path},
			CWD:          cloneString(info.CWD),
			PlatformOS:   info.PlatformOS,
			UserHomeDir:  strings.TrimSpace(info.UserHomeDir),
			Capabilities: info.Capabilities,
		}
		if err := response.Shell.Validate(); err != nil {
			return nil, err
		}
		return response, nil
	}
	conn, _, err := websocket.Dial(ctx, record.ExecServerURL, &websocket.DialOptions{HTTPClient: record.HTTPClient})
	if err != nil {
		return nil, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientName": "codex-go",
		},
	}); err != nil {
		return nil, err
	}
	if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "environment/info",
	}); err != nil {
		return nil, err
	}
	result, err := readExecServerResponse(ctx, conn, 2)
	if err != nil {
		return nil, err
	}
	var info EnvironmentInfoResponse
	if err := json.Unmarshal(result, &info); err != nil {
		return nil, err
	}
	if err := info.Shell.Validate(); err != nil {
		return nil, err
	}
	return &info, nil
}

func fetchRemoteEnvironmentStatus(ctx context.Context, record *EnvironmentRecord) (*EnvironmentStatusResponse, error) {
	if record == nil {
		return nil, errors.New("environment record is nil")
	}
	ctx, cancel := context.WithTimeout(ctx, environmentConnectTimeout(record.ConnectTimeoutMS))
	defer cancel()
	if record.NoiseProvider != nil {
		client, err := execserverclient.DialNoiseRendezvousClient(ctx, record.NoiseProvider, execserverclient.DialClientOptions{ClientName: "codex-go", HTTPClient: record.HTTPClient})
		if err != nil {
			return nil, err
		}
		defer client.Close()
		status, err := client.EnvironmentStatus(ctx)
		if err != nil {
			return nil, err
		}
		return &EnvironmentStatusResponse{Status: EnvironmentStatusKind(status.Status)}, nil
	}
	conn, _, err := websocket.Dial(ctx, record.ExecServerURL, &websocket.DialOptions{HTTPClient: record.HTTPClient})
	if err != nil {
		return nil, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"clientName": "codex-go",
		},
	}); err != nil {
		return nil, err
	}
	if _, err := readExecServerResponse(ctx, conn, 1); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
		}
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "initialized",
	}); err != nil {
		return nil, err
	}
	if err := writeExecServerJSON(ctx, conn, &execServerJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "environment/status",
	}); err != nil {
		return nil, err
	}
	result, err := readExecServerResponse(ctx, conn, 2)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &EnvironmentStatusResponse{Status: EnvironmentStatusPending}, nil
		}
		return nil, err
	}
	var status EnvironmentStatusResponse
	if err := json.Unmarshal(result, &status); err != nil {
		return nil, err
	}
	if status.Status == "" {
		status.Status = EnvironmentStatusReady
	}
	return &status, nil
}

func environmentConnectTimeout(connectTimeoutMS *uint64) time.Duration {
	if connectTimeoutMS == nil {
		return defaultEnvironmentConnectTimeout
	}
	timeout := time.Duration(*connectTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		return defaultEnvironmentConnectTimeout
	}
	return timeout
}

func writeExecServerJSON(ctx context.Context, conn *websocket.Conn, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func readExecServerResponse(ctx context.Context, conn *websocket.Conn, id int) (json.RawMessage, error) {
	for {
		messageType, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if messageType != websocket.MessageText && messageType != websocket.MessageBinary {
			continue
		}
		var response execServerJSONRPCResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return nil, err
		}
		if response.ID.String() != fmt.Sprint(id) {
			continue
		}
		if response.Error != nil {
			if strings.TrimSpace(response.Error.Message) != "" {
				return nil, errors.New(response.Error.Message)
			}
			return nil, fmt.Errorf("exec-server request %d failed with code %d", id, response.Error.Code)
		}
		if len(response.Result) == 0 {
			return []byte("{}"), nil
		}
		return response.Result, nil
	}
}
