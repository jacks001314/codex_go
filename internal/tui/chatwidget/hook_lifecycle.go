package chatwidget

type HookLifecycleState struct {
	Runs                       []HookRun
	ActiveRuns                 []HookRun
	CompletedPersistentRuns    []HookRun
	ActiveCellVisible          bool
	ActiveCellRevision         uint64
	UsageInsertionRequests     int
	NeedsFinalMessageSeparator bool
}

type HookLifecycleResult struct {
	RecordedVisibleTurnActivity bool
	FlushAnswerStream           bool
	InsertedCompletedRuns       []HookRun
	BumpedActiveCellRevision    bool
	NeedsFinalMessageSeparator  bool
	RequestedUsageInsertion     bool
	RequestRedraw               bool
	ActiveCellCleared           bool
}

func (s *HookLifecycleState) Start(run HookRun) {
	if s == nil {
		return
	}
	run.Status = HookStatusStarted
	s.upsert(run)
}

func (s *HookLifecycleState) Complete(run HookRun) {
	if s == nil {
		return
	}
	if run.Status == "" || run.Status == HookStatusStarted {
		run.Status = HookStatusCompleted
	}
	s.upsert(run)
}

func (s *HookLifecycleState) ClearActiveHookCell() HookLifecycleResult {
	result := HookLifecycleResult{}
	if s == nil || (!s.ActiveCellVisible && len(s.ActiveRuns) == 0 && len(s.CompletedPersistentRuns) == 0) {
		return result
	}
	s.ActiveRuns = nil
	s.CompletedPersistentRuns = nil
	s.ActiveCellVisible = false
	s.bumpActiveCellRevision(&result)
	s.requestUsageInsertion(&result)
	result.ActiveCellCleared = true
	return result
}

func (s *HookLifecycleState) OnHookStarted(run HookRun) HookLifecycleResult {
	result := HookLifecycleResult{
		RecordedVisibleTurnActivity: true,
		FlushAnswerStream:           true,
		RequestRedraw:               true,
	}
	if s == nil {
		return result
	}
	flush := s.FlushCompletedHookOutput()
	result.InsertedCompletedRuns = append(result.InsertedCompletedRuns, flush.InsertedCompletedRuns...)
	result.NeedsFinalMessageSeparator = flush.NeedsFinalMessageSeparator
	result.RequestedUsageInsertion = flush.RequestedUsageInsertion
	run.Status = HookStatusStarted
	s.ActiveCellVisible = true
	s.upsertActive(run)
	s.upsert(run)
	s.bumpActiveCellRevision(&result)
	return result
}

func (s *HookLifecycleState) OnHookCompleted(run HookRun) HookLifecycleResult {
	result := HookLifecycleResult{RequestRedraw: true}
	if s == nil {
		return result
	}
	if run.Status == "" || run.Status == HookStatusStarted {
		run.Status = HookStatusCompleted
	}
	completedExisting := s.completeActiveRun(run)
	if completedExisting {
		s.bumpActiveCellRevision(&result)
	} else {
		s.ActiveCellVisible = true
		s.CompletedPersistentRuns = append(s.CompletedPersistentRuns, run)
		s.bumpActiveCellRevision(&result)
	}
	s.upsert(run)
	flush := s.FlushCompletedHookOutput()
	result.InsertedCompletedRuns = append(result.InsertedCompletedRuns, flush.InsertedCompletedRuns...)
	result.NeedsFinalMessageSeparator = flush.NeedsFinalMessageSeparator
	result.RequestedUsageInsertion = flush.RequestedUsageInsertion
	if flush.BumpedActiveCellRevision {
		result.BumpedActiveCellRevision = true
	}
	finish := s.FinishActiveHookCellIfIdle()
	if finish.ActiveCellCleared {
		result.ActiveCellCleared = true
		result.RequestedUsageInsertion = true
	}
	if finish.BumpedActiveCellRevision {
		result.BumpedActiveCellRevision = true
	}
	return result
}

func (s *HookLifecycleState) FlushCompletedHookOutput() HookLifecycleResult {
	result := HookLifecycleResult{}
	if s == nil || len(s.CompletedPersistentRuns) == 0 {
		return result
	}
	result.InsertedCompletedRuns = append([]HookRun(nil), s.CompletedPersistentRuns...)
	s.CompletedPersistentRuns = nil
	s.bumpActiveCellRevision(&result)
	s.NeedsFinalMessageSeparator = true
	result.NeedsFinalMessageSeparator = true
	s.requestUsageInsertion(&result)
	return result
}

func (s *HookLifecycleState) FinishActiveHookCellIfIdle() HookLifecycleResult {
	result := HookLifecycleResult{}
	if s == nil || !s.ActiveCellVisible || len(s.ActiveRuns) != 0 {
		return result
	}
	if len(s.CompletedPersistentRuns) == 0 {
		s.ActiveCellVisible = false
		s.bumpActiveCellRevision(&result)
		s.requestUsageInsertion(&result)
		result.ActiveCellCleared = true
	}
	return result
}

func (s *HookLifecycleState) upsert(run HookRun) {
	if run.ID == "" {
		return
	}
	for i := range s.Runs {
		if s.Runs[i].ID == run.ID {
			s.Runs[i] = run
			return
		}
	}
	s.Runs = append(s.Runs, run)
}

func (s *HookLifecycleState) upsertActive(run HookRun) {
	if run.ID == "" {
		return
	}
	for i := range s.ActiveRuns {
		if s.ActiveRuns[i].ID == run.ID {
			s.ActiveRuns[i] = run
			return
		}
	}
	s.ActiveRuns = append(s.ActiveRuns, run)
}

func (s *HookLifecycleState) completeActiveRun(run HookRun) bool {
	for i := range s.ActiveRuns {
		if s.ActiveRuns[i].ID != run.ID {
			continue
		}
		s.ActiveRuns = append(s.ActiveRuns[:i], s.ActiveRuns[i+1:]...)
		s.CompletedPersistentRuns = append(s.CompletedPersistentRuns, run)
		return true
	}
	return false
}

func (s *HookLifecycleState) bumpActiveCellRevision(result *HookLifecycleResult) {
	s.ActiveCellRevision++
	if result != nil {
		result.BumpedActiveCellRevision = true
	}
}

func (s *HookLifecycleState) requestUsageInsertion(result *HookLifecycleResult) {
	s.UsageInsertionRequests++
	if result != nil {
		result.RequestedUsageInsertion = true
	}
}

func (s HookLifecycleState) BrowserView() SelectionView {
	return NewHooksBrowserView(s.Runs)
}
