package resources

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/reconciler/pipeline/dag"
)

type PipelineRunStateV2 []*ResolvedPipelineTaskV2

type PipelineRunFactsV2 struct {
	State           PipelineRunStateV2
	SpecStatus      v1.PipelineRunSpecStatus
	TasksGraph      *dag.Graph
	FinalTasksGraph *dag.Graph
	TimeoutsState   PipelineRunTimeoutsState

	// SkipCache is a hash of PipelineTask names that stores whether a task will be
	// executed or not, because it's either not reachable via the DAG due to the pipeline
	// state, or because it was skipped due to when expressions.
	// We cache this data along the state, because it's expensive to compute, it requires
	// traversing potentially the whole graph; this way it can built incrementally, when
	// needed, via the `Skip` method in pipelinerunresolution.go
	// The skip data is sensitive to changes in the state. The ResetSkippedCache method
	// can be used to clean the cache and force re-computation when needed.
	SkipCache map[string]TaskSkipStatus

	// ValidationFailedTask are the tasks for which taskrun is not created as they
	// never got added to the execution i.e. they failed in the validation step. One of
	// the case of failing at the validation is during CheckMissingResultReferences method
	// Tasks in ValidationFailedTask is added in method runNextSchedulableTask
	ValidationFailedTask []*ResolvedPipelineTaskV2
}
