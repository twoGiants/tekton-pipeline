package resources

import (
	"context"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun/resources"
)

type ResolvedResource interface {
	ResolveResource(ctx context.Context) error
	UpdateConditionFromVerificationResult(pr *v1.PipelineRun) error
}

// TODO(twoGiants): wrapping probably not needed, just ResolvedResource will be enough
type ResolvedPipelineTaskV2 struct {
	resolvedResource ResolvedResource
	legacy           ResolvedPipelineTask

	PipelineTask *v1.PipelineTask
	ResultsCache map[string][]string

	// EvaluatedCEL is used to store the results of evaluated CEL expression
	EvaluatedCEL map[string]bool
}

func NewResolvedPipelineTaskV2(
	pipelineTask *v1.PipelineTask,
	pipelineRun v1.PipelineRun,
	getChildPipelineRun GetPipelineRun,
	getRun GetRun,
	getTask resources.GetTask,
	getTaskRun resources.GetTaskRun,
) *ResolvedPipelineTaskV2 {
	var rr ResolvedResource
	if pipelineTask.PipelineSpec != nil {
		rr = NewResolvedChildPipelineRun(
			pipelineTask,
			pipelineRun,
			getChildPipelineRun,
		)
	} else if pipelineTask.TaskRef.IsCustomTask() || pipelineTask.TaskSpec.IsCustomTask() {
		rr = NewResolvedCustomRun(
			pipelineTask,
			pipelineRun,
			getRun,
		)
	} else {
		rr = NewResolvedTaskRun(
			pipelineTask,
			pipelineRun,
			getTask,
			getTaskRun,
		)
	}

	return &ResolvedPipelineTaskV2{
		PipelineTask:     pipelineTask,
		resolvedResource: rr,
		legacy:           ResolvedPipelineTask{PipelineTask: pipelineTask},
	}
}

func (rpt *ResolvedPipelineTaskV2) ResolveResource(ctx context.Context) error {
	return rpt.resolvedResource.ResolveResource(ctx)
}

func (rpt *ResolvedPipelineTaskV2) UpdateConditionFromVerificationResult(pr *v1.PipelineRun) error {
	return rpt.resolvedResource.UpdateConditionFromVerificationResult(pr)
}

func ResolvePipelineTaskV2(
	ctx context.Context,
	pipelineRun v1.PipelineRun,
	getChildPipelineRun GetPipelineRun,
	getTask resources.GetTask,
	getTaskRun resources.GetTaskRun,
	getRun GetRun,
	pipelineTask v1.PipelineTask,
	pst PipelineRunStateV2,
) (*ResolvedPipelineTaskV2, error) {
	rpt := NewResolvedPipelineTaskV2(
		&pipelineTask,
		pipelineRun,
		getChildPipelineRun,
		getRun,
		getTask,
		getTaskRun,
	)

	// We want to resolve all of the result references and ignore any errors at this point since there could be
	// instances where result references are missing here, but will be later skipped and resolved in
	// skipBecauseResultReferencesAreMissing. The final validation is handled in CheckMissingResultReferences.
	resolvedResultRefs, _, _ := ResolveResultRefs(pst, PipelineRunState{&rpt.legacy})
	if err := validateArrayResultsIndex(resolvedResultRefs); err != nil {
		return nil, err
	}

	ApplyTaskResults(PipelineRunState{&rpt.legacy}, resolvedResultRefs)

	if err := rpt.ResolveResource(ctx); err != nil {
		return nil, err
	}

	return rpt, nil
}
