package resources

import (
	"context"
	"fmt"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

type ResolvedChildPipelineRun struct {
	ChildPipelineRunNames []string
	ChildPipelineRuns     []*v1.PipelineRun
	ResolvedPipeline      ResolvedPipeline

	pipelineTask        *v1.PipelineTask
	pipelineRunName     string
	numCombinations     int
	getChildPipelineRun GetPipelineRun
	childReferences     []v1.ChildStatusReference
}

func NewResolvedChildPipelineRun(
	pipelineTask *v1.PipelineTask,
	pipelineRun v1.PipelineRun,
	getChildPipelineRun GetPipelineRun,
) *ResolvedChildPipelineRun {
	return &ResolvedChildPipelineRun{
		pipelineTask:        pipelineTask,
		pipelineRunName:     pipelineRun.Name,
		numCombinations:     matrixCombinations(pipelineTask),
		getChildPipelineRun: getChildPipelineRun,
		childReferences:     pipelineRun.Status.ChildReferences,
	}
}

// TODO(twoGiants): extract into common.go
func matrixCombinations(pipelineTask *v1.PipelineTask) int {
	result := 1
	if pipelineTask.IsMatrixed() {
		result = pipelineTask.Matrix.CountCombinations()
	}
	return result
}

func (rpr *ResolvedChildPipelineRun) UpdateConditionFromVerificationResult(pr *v1.PipelineRun) error {
	// to be implemented at some point
	return nil
}

func (rpr *ResolvedChildPipelineRun) ResolveResource(ctx context.Context) error {
	rpr.ChildPipelineRunNames = GetNamesOfChildPipelineRuns(
		rpr.childReferences,
		rpr.pipelineTask.Name,
		rpr.pipelineRunName,
		rpr.numCombinations,
	)

	for _, childPipelineRunName := range rpr.ChildPipelineRunNames {
		if err := rpr.setChildPipelineRunsAndResolvedPipeline(
			ctx,
			childPipelineRunName,
		); err != nil {
			return err
		}
	}

	return nil
}

// GetNamesOfChildPipelineRuns should return unique names for child (PinP) `PipelineRuns` if one has not already been
// defined, and the existing one otherwise.
func GetNamesOfChildPipelineRuns(childRefs []v1.ChildStatusReference, ptName, prName string, numberOfPipelineRuns int) []string {
	if pipelineRunNames := getChildPipelineRunNamesFromChildRefs(childRefs, ptName); pipelineRunNames != nil {
		return pipelineRunNames
	}
	return getNewRunNames(ptName, prName, numberOfPipelineRuns)
}

// ...
func getChildPipelineRunNamesFromChildRefs(childRefs []v1.ChildStatusReference, ptName string) []string {
	var childPipelineRunNames []string
	for _, cr := range childRefs {
		if cr.PipelineTaskName == ptName {
			if cr.Kind == pipeline.PipelineRunControllerName {
				childPipelineRunNames = append(childPipelineRunNames, cr.Name)
			}
		}
	}
	return childPipelineRunNames
}

func (rpr *ResolvedChildPipelineRun) setChildPipelineRunsAndResolvedPipeline(
	ctx context.Context,
	childPipelineRunName string,
) error {
	childPipelineRun, err := rpr.getChildPipelineRun(childPipelineRunName)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return fmt.Errorf("error retrieving child PipelineRun %s: %w", childPipelineRunName, err)
		}
	}
	if childPipelineRun != nil {
		rpr.ChildPipelineRuns = append(rpr.ChildPipelineRuns, childPipelineRun)
	}

	rp := ResolvedPipeline{}
	switch {
	case rpr.pipelineTask.PipelineSpec != nil:
		rp.PipelineSpec = rpr.pipelineTask.PipelineSpec
	case rpr.pipelineTask.PipelineRef != nil:
		return fmt.Errorf("PipelineRef for PipelineTask %q is not yet implemented", rpr.pipelineTask.Name)
	default:
		return fmt.Errorf("PipelineSpec in PipelineTask %q missing", rpr.pipelineTask.Name)
	}

	rpr.ResolvedPipeline = rp
	return nil
}
