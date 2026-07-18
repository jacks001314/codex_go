package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (m *Manager) enableDurable(ctx context.Context, params *EnableParams, backend *ManagerBackendOptions) (*EnableResponse, *StatusChangedNotification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := backend.ensureReady(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	m.ensureLocked()
	current := cloneManagerEnrollment(m.enrollment)
	serverName := m.serverName
	installationID := m.installationID
	m.mu.Unlock()

	auth, err := backend.AuthLoader(ctx)
	if err != nil {
		return nil, nil, err
	}
	if auth == nil {
		return nil, nil, ErrRemoteControlAuthRequired
	}
	target := cloneRemoteControlTarget(backend.Target)
	var enrollment *Enrollment
	if current != nil && current.AccountID == auth.AccountID {
		enrollment = current
	} else if backend.Store != nil {
		enrollment, err = LoadPersistedRemoteControlEnrollment(ctx, backend.Store, target, auth.AccountID, backend.AppServerClientName)
		if err != nil {
			return nil, nil, err
		}
		if enrollment != nil {
			enrollment.ServerName = serverName
		}
	}
	if enrollment == nil {
		enrollment, auth, err = m.enrollRemoteControlServerWithRecovery(ctx, backend, target, auth, installationID, serverName)
		if err != nil {
			return nil, nil, err
		}
	}

	rows, err := backend.Store.SetRemoteControlEnabled(ctx, target.WebSocketURL, auth.AccountID, backend.AppServerClientName, true)
	if err != nil {
		return nil, nil, err
	}
	if rows == 0 {
		enabled := true
		if err := UpdatePersistedRemoteControlEnrollment(ctx, backend.Store, target, auth.AccountID, backend.AppServerClientName, enrollment, &enabled); err != nil {
			return nil, nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLocked()
	previous := m.statusLocked()
	m.enrollment = cloneManagerEnrollment(enrollment)
	m.status = StatusConnecting
	if strings.TrimSpace(enrollment.EnvironmentID) != "" {
		envID := enrollment.EnvironmentID
		m.environmentID = &envID
	}
	notification := m.statusLocked()
	response := EnableResponseFromNotification(notification)
	if statusChangedNotificationEqual(previous, notification) {
		return response, nil, nil
	}
	return response, notification, nil
}

func (m *Manager) disableDurable(ctx context.Context, params *DisableParams, backend *ManagerBackendOptions) (*DisableResponse, *StatusChangedNotification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := backend.ensureReady(); err != nil {
		return nil, nil, err
	}
	auth, err := backend.AuthLoader(ctx)
	if err != nil {
		return nil, nil, err
	}
	if auth == nil {
		return nil, nil, ErrRemoteControlAuthRequired
	}
	_, err = backend.Store.SetRemoteControlEnabled(ctx, backend.Target.WebSocketURL, auth.AccountID, backend.AppServerClientName, false)
	if err != nil {
		return nil, nil, err
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

func (m *Manager) startPairingDurable(ctx context.Context, params *PairingStartParams, backend *ManagerBackendOptions) (*PairingStartResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	enrollment, auth, err := m.preparePairingEnrollment(ctx, backend, true)
	if err != nil {
		return nil, err
	}
	response, err := enrollment.StartPairing(ctx, params, pairingOptionsFromBackend(backend))
	if errors.Is(err, ErrRemoteControlPermissionDenied) {
		enrollment.ClearServerToken()
		if err := m.replaceCurrentEnrollment(enrollment); err != nil {
			return nil, err
		}
		if err := m.refreshRemoteControlServerWithRecovery(ctx, backend, auth, enrollment); err != nil {
			return nil, ErrRemoteControlPairingUnavailable
		}
		if err := m.replaceCurrentEnrollment(enrollment); err != nil {
			return nil, err
		}
		return enrollment.StartPairing(ctx, params, pairingOptionsFromBackend(backend))
	}
	if errors.Is(err, ErrRemoteControlNotFound) {
		replaced, err := m.enrollReplacement(ctx, backend, auth)
		if err != nil {
			return nil, err
		}
		return replaced.StartPairing(ctx, params, pairingOptionsFromBackend(backend))
	}
	return response, err
}

func (m *Manager) pairingStatusDurable(ctx context.Context, params *PairingStatusParams, backend *ManagerBackendOptions) (*PairingStatusResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	enrollment, auth, err := m.preparePairingEnrollment(ctx, backend, false)
	if err != nil {
		return nil, err
	}
	response, err := enrollment.PairingStatus(ctx, params, pairingOptionsFromBackend(backend))
	if errors.Is(err, ErrRemoteControlPermissionDenied) {
		enrollment.ClearServerToken()
		if err := m.replaceCurrentEnrollment(enrollment); err != nil {
			return nil, err
		}
		if err := m.refreshRemoteControlServerWithRecovery(ctx, backend, auth, enrollment); err != nil {
			return nil, ErrRemoteControlPairingUnavailable
		}
		if err := m.replaceCurrentEnrollment(enrollment); err != nil {
			return nil, err
		}
		return enrollment.PairingStatus(ctx, params, pairingOptionsFromBackend(backend))
	}
	if errors.Is(err, ErrRemoteControlNotFound) {
		if _, err := m.enrollReplacement(ctx, backend, auth); err != nil {
			return nil, err
		}
		return nil, ErrRemoteControlPairingUnavailable
	}
	return response, err
}

func (m *Manager) PrepareWebsocketEnrollmentContext(ctx context.Context) (*RemoteControlConnectionAuth, *Enrollment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if m == nil {
		return nil, nil, fmt.Errorf("%w: manager is nil", ErrInvalidRequest)
	}
	m.mu.Lock()
	backend := cloneManagerBackendOptions(m.backend)
	m.mu.Unlock()
	if backend == nil {
		return nil, nil, fmt.Errorf("%w: remote control backend is nil", ErrInvalidRequest)
	}
	return m.prepareWebsocketEnrollment(ctx, backend)
}

func (m *Manager) prepareWebsocketEnrollment(ctx context.Context, backend *ManagerBackendOptions) (*RemoteControlConnectionAuth, *Enrollment, error) {
	if err := backend.ensureReady(); err != nil {
		return nil, nil, err
	}
	auth, err := backend.AuthLoader(ctx)
	if err != nil {
		m.clearPreparedWebsocketEnrollment()
		return nil, nil, err
	}
	if auth == nil {
		m.clearPreparedWebsocketEnrollment()
		return nil, nil, ErrRemoteControlAuthRequired
	}
	target := cloneRemoteControlTarget(backend.Target)
	m.mu.Lock()
	m.ensureLocked()
	current := cloneManagerEnrollment(m.enrollment)
	serverName := m.serverName
	installationID := m.installationID
	if current != nil && current.AccountID != auth.AccountID {
		m.enrollment = nil
		m.environmentID = nil
		current = nil
	}
	m.mu.Unlock()

	enrollment := current
	if enrollment != nil {
		enrollment.RemoteControlTarget = cloneRemoteControlTarget(target)
		enrollment.ServerName = serverName
	}
	if enrollment == nil {
		enrollment, err = LoadPersistedRemoteControlEnrollment(ctx, backend.Store, target, auth.AccountID, backend.AppServerClientName)
		if err != nil {
			return nil, nil, err
		}
		if enrollment != nil {
			enrollment.ServerName = serverName
		}
	}
	if enrollment == nil {
		var recovered *RemoteControlConnectionAuth
		enrollment, recovered, err = m.enrollRemoteControlServerWithRecovery(ctx, backend, target, auth, installationID, serverName)
		if err != nil {
			return nil, nil, err
		}
		auth = recovered
		if err := UpdatePersistedRemoteControlEnrollment(ctx, backend.Store, target, auth.AccountID, backend.AppServerClientName, enrollment, nil); err != nil {
			return nil, nil, err
		}
	}
	if enrollment.ServerTokenRefreshRequirement() != ServerTokenRefreshNotNeeded {
		if err := m.refreshRemoteControlServerWithRecovery(ctx, backend, auth, enrollment); err != nil {
			if errors.Is(err, ErrRemoteControlNotFound) {
				enrollment, auth, err = m.enrollRemoteControlServerWithRecovery(ctx, backend, target, auth, installationID, serverName)
				if err != nil {
					return nil, nil, err
				}
			} else {
				_ = m.replaceCurrentEnrollment(enrollment)
				return nil, nil, err
			}
		}
		if err := UpdatePersistedRemoteControlEnrollment(ctx, backend.Store, target, auth.AccountID, backend.AppServerClientName, enrollment, nil); err != nil {
			return nil, nil, err
		}
	}
	if err := m.replaceCurrentEnrollment(enrollment); err != nil {
		return nil, nil, err
	}
	return auth, cloneManagerEnrollment(enrollment), nil
}

func (m *Manager) preparePairingEnrollment(ctx context.Context, backend *ManagerBackendOptions, createIfMissing bool) (*Enrollment, *RemoteControlConnectionAuth, error) {
	if err := backend.ensureReady(); err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	m.ensureLocked()
	status := m.status
	current := cloneManagerEnrollment(m.enrollment)
	m.mu.Unlock()
	if status == StatusDisabled {
		return nil, nil, fmt.Errorf("%w: remote control pairing requires remote control to be enabled", ErrInvalidRequest)
	}
	auth, err := backend.AuthLoader(ctx)
	if err != nil {
		return nil, nil, ErrRemoteControlPairingUnavailable
	}
	if auth == nil {
		return nil, nil, ErrRemoteControlPairingUnavailable
	}
	enrollment := current
	if enrollment == nil || enrollment.AccountID != auth.AccountID {
		if !createIfMissing {
			return nil, nil, ErrRemoteControlPairingUnavailable
		}
		enrollment, err = LoadPersistedRemoteControlEnrollment(ctx, backend.Store, backend.Target, auth.AccountID, backend.AppServerClientName)
		if err != nil {
			return nil, nil, err
		}
		if enrollment == nil {
			enrollment, err = m.enrollReplacement(ctx, backend, auth)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if enrollment.ServerTokenRefreshRequirement() != ServerTokenRefreshNotNeeded {
		if err := m.refreshRemoteControlServerWithRecovery(ctx, backend, auth, enrollment); err != nil {
			if errors.Is(err, ErrRemoteControlNotFound) && createIfMissing {
				enrollment, err = m.enrollReplacement(ctx, backend, auth)
				if err != nil {
					return nil, nil, err
				}
			} else {
				_ = m.replaceCurrentEnrollment(enrollment)
				return nil, nil, err
			}
		}
	}
	if err := m.replaceCurrentEnrollment(enrollment); err != nil {
		return nil, nil, err
	}
	return enrollment, auth, nil
}

func (m *Manager) enrollReplacement(ctx context.Context, backend *ManagerBackendOptions, auth *RemoteControlConnectionAuth) (*Enrollment, error) {
	if auth == nil {
		return nil, ErrRemoteControlAuthRequired
	}
	m.mu.Lock()
	m.ensureLocked()
	serverName := m.serverName
	installationID := m.installationID
	m.mu.Unlock()
	enrollment, auth, err := m.enrollRemoteControlServerWithRecovery(ctx, backend, backend.Target, auth, installationID, serverName)
	if err != nil {
		return nil, err
	}
	enabled := true
	if err := UpdatePersistedRemoteControlEnrollment(ctx, backend.Store, backend.Target, auth.AccountID, backend.AppServerClientName, enrollment, &enabled); err != nil {
		return nil, err
	}
	if err := m.replaceCurrentEnrollment(enrollment); err != nil {
		return nil, err
	}
	return enrollment, nil
}

func (m *Manager) clearPreparedWebsocketEnrollment() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enrollment = nil
	m.environmentID = nil
}

func (m *Manager) clearRemoteControlServerTokenIfMatches(enrollment *Enrollment) error {
	if m == nil || enrollment == nil {
		return fmt.Errorf("missing remote control enrollment after websocket auth failure")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.enrollment
	if !sameRemoteControlEnrollment(current, enrollment) {
		return fmt.Errorf("missing remote control enrollment after websocket auth failure")
	}
	if stringPtrEqual(current.RemoteControlToken, enrollment.RemoteControlToken) {
		current.ClearServerToken()
	}
	return nil
}

func (m *Manager) replaceWebsocketEnrollmentIfMatches(ctx context.Context, backend *ManagerBackendOptions, auth *RemoteControlConnectionAuth, stale *Enrollment) error {
	if m == nil || backend == nil || auth == nil || stale == nil {
		return fmt.Errorf("%w: remote control enrollment replacement requires manager, backend, auth, and enrollment", ErrInvalidRequest)
	}
	m.mu.Lock()
	m.ensureLocked()
	current := cloneManagerEnrollment(m.enrollment)
	serverName := m.serverName
	installationID := m.installationID
	m.mu.Unlock()
	if !sameRemoteControlEnrollment(current, stale) {
		return nil
	}

	target := cloneRemoteControlTarget(backend.Target)
	replacement, recovered, err := m.enrollRemoteControlServerWithRecovery(ctx, backend, target, auth, installationID, serverName)
	if err != nil {
		return err
	}
	enabled := true
	if err := UpdatePersistedRemoteControlEnrollment(ctx, backend.Store, target, recovered.AccountID, backend.AppServerClientName, replacement, &enabled); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !sameRemoteControlEnrollment(m.enrollment, stale) {
		return nil
	}
	m.enrollment = cloneManagerEnrollment(replacement)
	if strings.TrimSpace(replacement.EnvironmentID) != "" {
		envID := replacement.EnvironmentID
		m.environmentID = &envID
	}
	return nil
}

func (m *Manager) enrollRemoteControlServerWithRecovery(ctx context.Context, backend *ManagerBackendOptions, target *RemoteControlTarget, auth *RemoteControlConnectionAuth, installationID string, serverName string) (*Enrollment, *RemoteControlConnectionAuth, error) {
	enrollment, err := EnrollRemoteControlServer(ctx, target, auth, installationID, serverName, backend.ServerAPIOptions)
	if err == nil {
		return enrollment, auth, nil
	}
	if !errors.Is(err, ErrRemoteControlPermissionDenied) || backend.AuthRecovery == nil {
		return nil, auth, err
	}
	recovered, ok, recoverErr := backend.AuthRecovery(ctx, auth)
	if recoverErr != nil || !ok || recovered == nil {
		if recoverErr != nil {
			return nil, auth, err
		}
		return nil, auth, err
	}
	enrollment, retryErr := EnrollRemoteControlServer(ctx, target, recovered, installationID, serverName, backend.ServerAPIOptions)
	if retryErr != nil {
		return nil, recovered, retryErr
	}
	return enrollment, recovered, nil
}

func (m *Manager) refreshRemoteControlServerWithRecovery(ctx context.Context, backend *ManagerBackendOptions, auth *RemoteControlConnectionAuth, enrollment *Enrollment) error {
	err := RefreshRemoteControlServer(ctx, auth, m.installationIDSnapshot(), enrollment, backend.ServerAPIOptions)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrRemoteControlPermissionDenied) || backend.AuthRecovery == nil {
		return err
	}
	recovered, ok, recoverErr := backend.AuthRecovery(ctx, auth)
	if recoverErr != nil || !ok || recovered == nil {
		return err
	}
	if recovered.AccountID != enrollment.AccountID {
		enrollment.ClearServerToken()
		return ErrRemoteControlPairingUnavailable
	}
	return RefreshRemoteControlServer(ctx, recovered, m.installationIDSnapshot(), enrollment, backend.ServerAPIOptions)
}

func (m *Manager) replaceCurrentEnrollment(enrollment *Enrollment) error {
	if enrollment == nil {
		return ErrRemoteControlPairingUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enrollment != nil && m.enrollment.AccountID != enrollment.AccountID {
		return ErrRemoteControlPairingUnavailable
	}
	m.enrollment = cloneManagerEnrollment(enrollment)
	if strings.TrimSpace(enrollment.EnvironmentID) != "" && m.status != StatusDisabled {
		envID := enrollment.EnvironmentID
		m.environmentID = &envID
	}
	return nil
}

func (m *Manager) installationIDSnapshot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.installationID
}

func pairingOptionsFromBackend(backend *ManagerBackendOptions) *PairingOptions {
	if backend == nil || backend.ServerAPIOptions == nil {
		return nil
	}
	return &PairingOptions{
		HTTPClient: backend.ServerAPIOptions.HTTPClient,
		Timeout:    backend.ServerAPIOptions.Timeout,
	}
}

func (b *ManagerBackendOptions) ensureReady() error {
	if b == nil {
		return nil
	}
	if b.Target == nil {
		if strings.TrimSpace(b.RemoteControlURL) == "" {
			return fmt.Errorf("%w: remote control target is nil", ErrInvalidRequest)
		}
		target, err := NormalizeRemoteControlURL(b.RemoteControlURL)
		if err != nil {
			return err
		}
		b.Target = target
	}
	if b.Store == nil {
		return fmt.Errorf("remote control cannot be enabled because sqlite state db is unavailable")
	}
	if b.AuthLoader == nil {
		return fmt.Errorf("%w: auth loader is nil", ErrInvalidRequest)
	}
	return nil
}

func (b *ManagerBackendOptions) ensureClientManagementReady() error {
	if b == nil {
		return fmt.Errorf("%w: remote control backend is nil", ErrInvalidRequest)
	}
	if b.Target == nil {
		if strings.TrimSpace(b.RemoteControlURL) == "" {
			return fmt.Errorf("%w: remote control target is nil", ErrInvalidRequest)
		}
		target, err := NormalizeRemoteControlURL(b.RemoteControlURL)
		if err != nil {
			return err
		}
		b.Target = target
	}
	if b.AuthLoader == nil {
		return fmt.Errorf("%w: auth loader is nil", ErrInvalidRequest)
	}
	return nil
}

func cloneManagerBackendOptions(backend *ManagerBackendOptions) *ManagerBackendOptions {
	if backend == nil {
		return nil
	}
	clone := *backend
	clone.Target = cloneRemoteControlTarget(backend.Target)
	clone.AppServerClientName = cloneStringPtr(backend.AppServerClientName)
	if clone.ServerAPIOptions != nil {
		options := *clone.ServerAPIOptions
		clone.ServerAPIOptions = &options
	}
	return &clone
}

func sameRemoteControlEnrollment(left *Enrollment, right *Enrollment) bool {
	if left == nil || right == nil {
		return false
	}
	return left.AccountID == right.AccountID &&
		left.ServerID == right.ServerID &&
		left.EnvironmentID == right.EnvironmentID
}

func cloneManagerEnrollment(enrollment *Enrollment) *Enrollment {
	if enrollment == nil {
		return nil
	}
	clone := *enrollment
	clone.RemoteControlTarget = cloneRemoteControlTarget(enrollment.RemoteControlTarget)
	clone.RemoteControlToken = cloneStringPtr(enrollment.RemoteControlToken)
	if enrollment.ExpiresAt != nil {
		value := *enrollment.ExpiresAt
		clone.ExpiresAt = &value
	}
	if enrollment.NextRefreshAt != nil {
		value := *enrollment.NextRefreshAt
		clone.NextRefreshAt = &value
	}
	return &clone
}
