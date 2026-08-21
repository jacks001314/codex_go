package chatwidget

import (
	"strings"
	"time"

	historycell "codex_go/tui/history_cell"
	streamingcore "codex_go/tui/streaming"
)

type ChatStreamTailKind string

const (
	ChatStreamTailNone   ChatStreamTailKind = ""
	ChatStreamTailAnswer ChatStreamTailKind = "answer"
	ChatStreamTailPlan   ChatStreamTailKind = "plan"
)

type ChatStreamTail struct {
	Kind         ChatStreamTailKind
	Lines        []string
	StartsStream bool
}

type ChatStreamingState struct {
	MessageDeltaCount   int
	ReasoningDeltaCount int
	PlanDeltaCount      int

	Width                  int
	PlanMode               bool
	TaskRunning            bool
	TaskCompletePending    bool
	StatusIndicatorVisible bool
	PendingStatusRestore   bool
	StatusHeader           string
	StatusKind             string
	ReasoningBuffer        string
	FullReasoningBuffer    string
	PlanDeltaBuffer        string
	PlanItemActive         bool
	SawPlanItemThisTurn    bool
	LatestProposedPlan     string
	FinalizedAnswerSource  string
	FinalizedPlanSource    string
	VisibleTurnActivity    int
	CommitAnimationStarts  int
	CommitAnimationStops   int
	UsageInsertionRequests int
	History                []historycell.HistoryCell
	ActiveTail             ChatStreamTail
	StreamController       *streamingcore.StreamController
	PlanStreamController   *streamingcore.PlanStreamController
	AdaptiveChunking       *streamingcore.AdaptiveChunkingPolicy
}

func NewChatStreamingState(width int) *ChatStreamingState {
	if width <= 0 {
		width = 80
	}
	return &ChatStreamingState{
		Width:                  width,
		StatusIndicatorVisible: true,
		AdaptiveChunking:       streamingcore.NewAdaptiveChunkingPolicy(),
	}
}

func (s *ChatStreamingState) OnAgentMessageDelta(delta string) {
	if s == nil {
		return
	}
	if delta != "" {
		s.MessageDeltaCount++
	}
	s.handleStreamingDelta(delta)
}

func (s *ChatStreamingState) OnReasoningDelta(delta string) {
	if s == nil {
		return
	}
	if delta != "" {
		s.ReasoningDeltaCount++
	}
	s.OnAgentReasoningDelta(delta)
}

func (s *ChatStreamingState) OnPlanDelta(delta string) {
	if s == nil {
		return
	}
	if delta != "" {
		s.PlanDeltaCount++
	}
	if !s.PlanMode {
		return
	}
	if delta != "" {
		s.recordVisibleTurnActivity()
	}
	if !s.PlanItemActive {
		s.PlanItemActive = true
		s.PlanDeltaBuffer = ""
	}
	s.PlanDeltaBuffer += delta
	s.ensureDefaults()
	if s.PlanStreamController == nil {
		s.PlanStreamController = streamingcore.NewPlanStreamController(s.streamWidth(4))
	}
	if s.PlanStreamController.Push(delta) {
		s.startCommitAnimation()
		s.RunCatchUpCommitTick(time.Now())
	}
	s.SyncActiveStreamTail()
}

func (s ChatStreamingState) HasActivity() bool {
	return s.MessageDeltaCount > 0 || s.ReasoningDeltaCount > 0 || s.PlanDeltaCount > 0 || s.VisibleTurnActivity > 0
}

func (s *ChatStreamingState) RestoreReasoningStatusHeader() {
	if s == nil {
		return
	}
	if header, ok := ExtractFirstBold(s.ReasoningBuffer); ok {
		s.StatusKind = "thinking"
		s.StatusHeader = header
		return
	}
	if s.TaskRunning {
		s.StatusKind = "working"
		s.StatusHeader = "Working"
	}
}

func (s *ChatStreamingState) FinalizeCompletedAssistantMessage(message string) {
	if s == nil {
		return
	}
	if s.StreamController == nil && message != "" {
		s.handleStreamingDelta(message)
	}
	s.FlushAnswerStreamWithSeparator()
	s.HandleStreamFinished()
}

func (s *ChatStreamingState) FlushAnswerStreamWithSeparator() {
	if s == nil {
		return
	}
	hadController := s.StreamController != nil
	if s.StreamController != nil {
		controller := s.StreamController
		hadLiveTail := controller.HasLiveTail()
		s.StreamController = nil
		s.ClearActiveStreamTail()
		cell, source := controller.Finalize()
		s.FinalizedAnswerSource = source
		if cell != nil && !hadLiveTail {
			s.addHistory(cell)
		} else if cell != nil {
			s.addHistory(cell)
		}
	}
	s.ensureDefaults()
	s.AdaptiveChunking.Reset()
	if hadController && s.StreamControllersIdle() {
		s.stopCommitAnimation()
	}
	if hadController {
		s.UsageInsertionRequests++
	}
}

func (s *ChatStreamingState) StreamControllersIdle() bool {
	if s == nil {
		return true
	}
	return (s.StreamController == nil || s.StreamController.QueuedLines() == 0) &&
		(s.PlanStreamController == nil || s.PlanStreamController.QueuedLines() == 0)
}

func (s *ChatStreamingState) MaybeRestoreStatusIndicatorAfterStreamIdle() {
	if s == nil || !s.PendingStatusRestore || !s.TaskRunning || !s.StreamControllersIdle() {
		return
	}
	s.StatusIndicatorVisible = true
	if s.StatusHeader == "" {
		s.StatusHeader = "Working"
		s.StatusKind = "working"
	}
	s.PendingStatusRestore = false
}

func (s *ChatStreamingState) OnPlanItemCompleted(text string) {
	if s == nil {
		return
	}
	streamedPlan := strings.TrimSpace(s.PlanDeltaBuffer)
	planText := strings.TrimSpace(text)
	if planText == "" {
		planText = streamedPlan
	}
	if planText != "" {
		s.LatestProposedPlan = planText
	}
	shouldRestore := s.PlanStreamController != nil
	s.PlanDeltaBuffer = ""
	s.PlanItemActive = false
	s.SawPlanItemThisTurn = true
	var finalized historycell.HistoryCell
	var source string
	if s.PlanStreamController != nil {
		controller := s.PlanStreamController
		s.PlanStreamController = nil
		s.ClearActiveStreamTail()
		finalized, source = controller.Finalize()
		s.FinalizedPlanSource = source
	}
	if finalized != nil {
		s.addHistory(finalized)
	} else if planText != "" {
		s.addHistory(historycell.NewProposedPlan(planText))
	}
	if shouldRestore {
		s.PendingStatusRestore = true
		s.MaybeRestoreStatusIndicatorAfterStreamIdle()
		s.UsageInsertionRequests++
	}
}

func (s *ChatStreamingState) OnAgentReasoningDelta(delta string) {
	if s == nil {
		return
	}
	s.ReasoningBuffer += delta
	if header, ok := ExtractFirstBold(s.ReasoningBuffer); ok {
		s.StatusKind = "thinking"
		s.StatusHeader = header
	}
}

func (s *ChatStreamingState) OnAgentReasoningFinal() {
	if s == nil {
		return
	}
	s.FullReasoningBuffer += s.ReasoningBuffer
	if strings.TrimSpace(s.FullReasoningBuffer) != "" {
		s.addHistory(historycell.NewReasoningSummaryCell(s.FullReasoningBuffer, false))
	}
	s.ReasoningBuffer = ""
	s.FullReasoningBuffer = ""
}

func (s *ChatStreamingState) OnReasoningSectionBreak() {
	if s == nil {
		return
	}
	s.FullReasoningBuffer += s.ReasoningBuffer
	s.FullReasoningBuffer += "\n\n"
	s.ReasoningBuffer = ""
}

func (s *ChatStreamingState) OnStreamError(message string, additionalDetails string) {
	if s == nil {
		return
	}
	s.StatusIndicatorVisible = true
	s.StatusKind = "thinking"
	s.StatusHeader = strings.TrimSpace(message)
	if strings.TrimSpace(additionalDetails) != "" {
		s.StatusHeader = strings.TrimSpace(message) + ": " + strings.TrimSpace(additionalDetails)
	}
}

func (s *ChatStreamingState) RunCommitTick(now time.Time) {
	s.RunCommitTickWithScope(streamingcore.CommitTickAnyMode, now)
}

func (s *ChatStreamingState) RunCatchUpCommitTick(now time.Time) {
	s.RunCommitTickWithScope(streamingcore.CommitTickCatchUpOnly, now)
}

func (s *ChatStreamingState) RunCommitTickWithScope(scope streamingcore.CommitTickScope, now time.Time) {
	if s == nil {
		return
	}
	s.ensureDefaults()
	outcome := streamingcore.RunCommitTick(s.AdaptiveChunking, s.StreamController, s.PlanStreamController, scope, now)
	for _, cell := range outcome.Cells {
		s.StatusIndicatorVisible = false
		s.addHistory(cell)
	}
	s.SyncActiveStreamTail()
	if outcome.HasController && outcome.AllIdle {
		s.MaybeRestoreStatusIndicatorAfterStreamIdle()
		s.stopCommitAnimation()
	}
}

func (s *ChatStreamingState) HandleStreamFinished() {
	if s == nil {
		return
	}
	if s.TaskCompletePending {
		s.StatusIndicatorVisible = false
		s.TaskCompletePending = false
	}
}

func (s *ChatStreamingState) ActiveCellIsStreamTail() bool {
	return s != nil && s.ActiveTail.Kind != ChatStreamTailNone
}

func (s *ChatStreamingState) HasActiveStreamTail() bool {
	return s != nil &&
		(s.StreamController != nil || s.PlanStreamController != nil) &&
		s.ActiveCellIsStreamTail()
}

func (s *ChatStreamingState) SyncActiveStreamTail() {
	if s == nil {
		return
	}
	if s.StreamController != nil {
		lines := s.StreamController.CurrentTailLines()
		if len(lines) == 0 {
			s.ClearActiveStreamTail()
			return
		}
		s.StatusIndicatorVisible = false
		s.ActiveTail = ChatStreamTail{Kind: ChatStreamTailAnswer, Lines: lines, StartsStream: s.StreamController.TailStartsStream()}
		return
	}
	if s.PlanStreamController != nil {
		lines := s.PlanStreamController.CurrentTailDisplayLines()
		if len(lines) == 0 {
			s.ClearActiveStreamTail()
			return
		}
		s.StatusIndicatorVisible = false
		s.ActiveTail = ChatStreamTail{Kind: ChatStreamTailPlan, Lines: lines, StartsStream: false}
		return
	}
	s.ClearActiveStreamTail()
}

func (s *ChatStreamingState) ClearActiveStreamTail() {
	if s != nil {
		s.ActiveTail = ChatStreamTail{}
	}
}

func ExtractFirstBold(markdown string) (string, bool) {
	start := strings.Index(markdown, "**")
	if start < 0 {
		return "", false
	}
	rest := markdown[start+2:]
	end := strings.Index(rest, "**")
	if end < 0 {
		return "", false
	}
	header := strings.TrimSpace(rest[:end])
	if header == "" {
		return "", false
	}
	return header, true
}

func (s *ChatStreamingState) handleStreamingDelta(delta string) {
	s.ensureDefaults()
	if delta != "" {
		s.recordVisibleTurnActivity()
	}
	if s.StreamController == nil {
		s.StreamController = streamingcore.NewStreamControllerWithTheme(s.streamWidth(2), streamingcore.CurrentStreamTheme())
	}
	s.StreamController.SetTheme(streamingcore.CurrentStreamTheme())
	if s.StreamController.Push(delta) {
		s.startCommitAnimation()
		s.RunCatchUpCommitTick(time.Now())
	}
	s.SyncActiveStreamTail()
}

func (s *ChatStreamingState) streamWidth(reservedCols int) int {
	if s.Width <= 0 {
		s.Width = 80
	}
	width := s.Width - reservedCols
	if width < 1 {
		return 1
	}
	return width
}

func (s *ChatStreamingState) ensureDefaults() {
	if s.Width <= 0 {
		s.Width = 80
	}
	if s.AdaptiveChunking == nil {
		s.AdaptiveChunking = streamingcore.NewAdaptiveChunkingPolicy()
	}
}

func (s *ChatStreamingState) recordVisibleTurnActivity() {
	s.VisibleTurnActivity++
}

func (s *ChatStreamingState) addHistory(cell historycell.HistoryCell) {
	if cell != nil {
		s.History = append(s.History, cell)
	}
}

func (s *ChatStreamingState) startCommitAnimation() {
	s.CommitAnimationStarts++
}

func (s *ChatStreamingState) stopCommitAnimation() {
	s.CommitAnimationStops++
}
