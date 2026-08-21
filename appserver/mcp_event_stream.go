package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Rust #39761: experimental mcpServer/event/stream/start|stop requests and
// mcpServer/event/stream/notification forwarding for hosted apps. The Go
// surface mirrors app-server request_processors/mcp_event_stream.rs.

const (
	maxMCPEventStreamsPerConnection   = 64
	maxMCPEventStreamReconnectAttempt = 3
	mcpEventStreamReconnectDelay      = time.Second
	mcpEventStreamStartupTimeout      = 90 * time.Second
)

type McpServerEventStreamStartParams struct {
	ThreadID       string          `json:"threadId"`
	Server         string          `json:"server"`
	SubscriptionID string          `json:"subscriptionId"`
	Name           string          `json:"name"`
	Arguments      json.RawMessage `json:"arguments"`
	Meta           json.RawMessage `json:"_meta,omitempty"`
}

type McpServerEventStreamStartResponse struct{}

type McpServerEventStreamStopParams struct {
	SubscriptionID string `json:"subscriptionId"`
}

type McpServerEventStreamStopResponse struct{}

type McpServerEventNotification struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type McpServerEventStreamNotification struct {
	SubscriptionID string                     `json:"subscriptionId"`
	Notification   McpServerEventNotification `json:"notification"`
}

type mcpEventStreamEntry struct {
	threadID     string
	connectionID string
	cancel       context.CancelFunc
	done         chan struct{}
}

// mcpEventStreamManager scopes MCP event subscriptions to the owning
// app-server connection and subscribed thread (Rust McpEventStreams).
type mcpEventStreamManager struct {
	mu    sync.Mutex
	tasks map[string]*mcpEventStreamEntry
}

func newMCPEventStreamManager() *mcpEventStreamManager {
	return &mcpEventStreamManager{tasks: map[string]*mcpEventStreamEntry{}}
}

func (m *mcpEventStreamManager) activeCountForConnection(connectionID string) int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, entry := range m.tasks {
		if entry.connectionID == connectionID {
			count++
		}
	}
	return count
}

func (m *mcpEventStreamManager) insert(subscriptionID string, entry *mcpEventStreamEntry) bool {
	if m == nil || entry == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tasks[subscriptionID]; exists {
		return false
	}
	m.tasks[subscriptionID] = entry
	return true
}

func (m *mcpEventStreamManager) remove(subscriptionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.tasks, subscriptionID)
	m.mu.Unlock()
}

func (m *mcpEventStreamManager) stopConnection(connectionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	var entries []*mcpEventStreamEntry
	for _, entry := range m.tasks {
		if entry.connectionID == connectionID {
			entries = append(entries, entry)
		}
	}
	m.mu.Unlock()
	for _, entry := range entries {
		entry.cancel()
		<-entry.done
	}
}

func (m *mcpEventStreamManager) stopThreadConnection(threadID string, connectionID string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	var entries []*mcpEventStreamEntry
	for _, entry := range m.tasks {
		if entry.threadID == threadID && entry.connectionID == connectionID {
			entries = append(entries, entry)
		}
	}
	m.mu.Unlock()
	for _, entry := range entries {
		entry.cancel()
		<-entry.done
	}
}

func (m *mcpEventStreamManager) clear() {
	if m == nil {
		return
	}
	m.mu.Lock()
	entries := make([]*mcpEventStreamEntry, 0, len(m.tasks))
	for _, entry := range m.tasks {
		entries = append(entries, entry)
	}
	m.tasks = map[string]*mcpEventStreamEntry{}
	m.mu.Unlock()
	for _, entry := range entries {
		entry.cancel()
		<-entry.done
	}
}

func (r *RuntimeRouter) handleMCPServerEventStreamStart(request *Request) (*McpServerEventStreamStartResponse, error) {
	var params McpServerEventStreamStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.Server = strings.TrimSpace(params.Server)
	params.SubscriptionID = strings.TrimSpace(params.SubscriptionID)
	params.Name = strings.TrimSpace(params.Name)
	if params.Server != codexAppsMCPServerName {
		return nil, jsonRPCInvalidRequest("MCP event subscriptions are only supported for hosted apps")
	}
	if !validUUIDString(params.ThreadID) {
		return nil, jsonRPCInvalidRequest("invalid thread id: " + params.ThreadID)
	}
	if params.SubscriptionID == "" || params.Name == "" {
		return nil, jsonRPCInvalidRequest("subscriptionId and name are required")
	}
	connectionID := request.normalizedConnectionID()
	if !connectionSubscribedToThread(r, params.ThreadID, connectionID) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("connection is not subscribed to thread '%s'", params.ThreadID))
	}
	if r.mcpEventStreams.activeCountForConnection(connectionID) >= maxMCPEventStreamsPerConnection {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("MCP event subscription limit of %d reached", maxMCPEventStreamsPerConnection))
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry := &mcpEventStreamEntry{
		threadID:     params.ThreadID,
		connectionID: connectionID,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	entryKey := params.SubscriptionID
	if !r.mcpEventStreams.insert(entryKey, entry) {
		cancel()
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("MCP event subscription '%s' already exists", entryKey))
	}
	ready := make(chan error, 1)
	go r.runMCPEventStream(ctx, entry, entryKey, params, ready)
	select {
	case err := <-ready:
		if err != nil {
			entry.cancel()
			<-entry.done
			return nil, err
		}
		return &McpServerEventStreamStartResponse{}, nil
	case <-time.After(mcpEventStreamStartupTimeout):
		entry.cancel()
		<-entry.done
		return nil, jsonRPCInvalidRequest("MCP event stream startup timed out")
	}
}

func (r *RuntimeRouter) handleMCPServerEventStreamStop(request *Request) (*McpServerEventStreamStopResponse, error) {
	var params McpServerEventStreamStopParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	params.SubscriptionID = strings.TrimSpace(params.SubscriptionID)
	if params.SubscriptionID == "" {
		return nil, jsonRPCInvalidRequest("subscriptionId is required")
	}
	r.mcpEventStreams.mu.Lock()
	entry := r.mcpEventStreams.tasks[params.SubscriptionID]
	r.mcpEventStreams.mu.Unlock()
	if entry == nil {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("MCP event subscription '%s' not found", params.SubscriptionID))
	}
	entry.cancel()
	<-entry.done
	return &McpServerEventStreamStopResponse{}, nil
}

func connectionSubscribedToThread(r *RuntimeRouter, threadID string, connectionID string) bool {
	if r == nil || r.threads == nil {
		return false
	}
	for _, id := range r.subscribedConnectionIDsForThread(threadID) {
		if id == connectionID {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) runMCPEventStream(ctx context.Context, entry *mcpEventStreamEntry, subscriptionID string, params McpServerEventStreamStartParams, ready chan<- error) {
	defer close(entry.done)
	defer r.mcpEventStreams.remove(subscriptionID)
	service := r.mcpServiceForThread(params.ThreadID, nil)
	if service == nil {
		ready <- jsonRPCInvalidRequest("MCP runtime is not available")
		return
	}
	authChanged := r.authChangedChannel()
	var arguments any
	if len(params.Arguments) > 0 && string(params.Arguments) != "null" {
		_ = json.Unmarshal(params.Arguments, &arguments)
	}
	reconnectAttempts := 0
	var reconnectDeadline <-chan time.Time
	active := false
	sendTerminated := func() {
		r.sendMCPEventNotification(entry.connectionID, subscriptionID, "notifications/events/terminated", json.RawMessage(`{}`))
	}
	for {
		stream, err := service.OpenMCPEventStream(codexAppsMCPServerName, params.Name, arguments)
		if err != nil {
			if !active && reconnectAttempts < maxMCPEventStreamReconnectAttempt {
				reconnectAttempts++
				if reconnectDeadline == nil {
					reconnectDeadline = time.After(mcpEventStreamStartupTimeout)
				}
				select {
				case <-ctx.Done():
					return
				case <-authChanged:
					ready <- jsonRPCInvalidRequest("MCP event subscription authentication changed during startup")
					return
				case <-reconnectDeadline:
					ready <- jsonRPCInvalidRequest("MCP event stream startup timed out")
					return
				case <-time.After(mcpEventStreamReconnectDelay << (reconnectAttempts - 1)):
					continue
				}
			}
			ready <- jsonRPCInvalidRequest(fmt.Sprintf("failed to start MCP event stream for '%s': %v", params.Server, err))
			return
		}
		streamEnded := false
		for !streamEnded {
			select {
			case <-ctx.Done():
				stream.Close()
				if !active {
					ready <- jsonRPCInvalidRequest("MCP event stream ended before becoming active")
				}
				return
			case <-authChanged:
				stream.Close()
				if !active {
					ready <- jsonRPCInvalidRequest("MCP event subscription authentication changed during startup")
				}
				return
			case notification, ok := <-stream.Notifications():
				if !ok {
					streamEnded = true
					break
				}
				method := strings.TrimSpace(notification.Method)
				rawParams := json.RawMessage(`{}`)
				if len(notification.Params) > 0 && string(notification.Params) != "null" {
					rawParams = notification.Params
				}
				if !r.sendMCPEventNotification(entry.connectionID, subscriptionID, method, rawParams) {
					stream.Close()
					if !active {
						ready <- jsonRPCInvalidRequest("MCP event subscription connection closed")
					}
					return
				}
				if method == "notifications/events/active" {
					active = true
					reconnectAttempts = 0
					reconnectDeadline = nil
					ready <- nil
					continue
				}
				if method == "notifications/events/terminated" {
					stream.Close()
					return
				}
			case <-stream.Done():
				streamEnded = true
			}
		}
		stream.Close()
		if active {
			sendTerminated()
			return
		}
		if reconnectAttempts >= maxMCPEventStreamReconnectAttempt {
			ready <- jsonRPCInvalidRequest("MCP event stream ended before becoming active")
			return
		}
		reconnectAttempts++
		if reconnectDeadline == nil {
			reconnectDeadline = time.After(mcpEventStreamStartupTimeout)
		}
		select {
		case <-ctx.Done():
			ready <- jsonRPCInvalidRequest("MCP event stream ended before becoming active")
			return
		case <-authChanged:
			ready <- jsonRPCInvalidRequest("MCP event subscription authentication changed during startup")
			return
		case <-reconnectDeadline:
			ready <- jsonRPCInvalidRequest("MCP event stream startup timed out")
			return
		case <-time.After(mcpEventStreamReconnectDelay << (reconnectAttempts - 1)):
		}
	}
}

func (r *RuntimeRouter) sendMCPEventNotification(connectionID string, subscriptionID string, method string, rawParams json.RawMessage) bool {
	if r == nil {
		return false
	}
	r.notifyToConnection(connectionID, NotificationMCPServerEventStream, &McpServerEventStreamNotification{
		SubscriptionID: subscriptionID,
		Notification: McpServerEventNotification{
			Method: method,
			Params: rawParams,
		},
	})
	return true
}
