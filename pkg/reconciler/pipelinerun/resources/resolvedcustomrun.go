package resources

import (
	"context"
	"fmt"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

type ResolvedCustomRun struct {
	CustomRunNames []string
	CustomRuns     []*v1beta1.CustomRun

	pipelineTask    *v1.PipelineTask
	pipelineRunName string
	getRun          GetRun
	numCombinations int
	childReferences []v1.ChildStatusReference
}

func NewResolvedCustomRun(
	pipelineTask *v1.PipelineTask,
	pipelineRun v1.PipelineRun,
	getRun GetRun,
) *ResolvedCustomRun {
	return &ResolvedCustomRun{
		pipelineTask:    pipelineTask,
		pipelineRunName: pipelineRun.Name,
		numCombinations: matrixCombinations(pipelineTask),
		getRun:          getRun,
		childReferences: pipelineRun.Status.ChildReferences,
	}
}

func (rcr *ResolvedCustomRun) UpdateConditionFromVerificationResult(pr *v1.PipelineRun) error {
	// do nothing in custom run
	return nil
}

func (rcr *ResolvedCustomRun) ResolveResource(ctx context.Context) error {
	rcr.CustomRunNames = getNamesOfCustomRuns(
		rcr.childReferences,
		rcr.pipelineTask.Name,
		rcr.pipelineRunName,
		rcr.numCombinations)
	for _, runName := range rcr.CustomRunNames {
		run, err := rcr.getRun(runName)
		if err != nil && !kerrors.IsNotFound(err) {
			return fmt.Errorf("error retrieving CustomRun %s: %w", runName, err)
		}
		if run != nil {
			rcr.CustomRuns = append(rcr.CustomRuns, run)
		}
	}

	return nil
}

// getNamesOfCustomRuns should return a unique names for `CustomRuns` if they have not already been defined,
// and the existing ones otherwise.
func getNamesOfCustomRuns(childRefs []v1.ChildStatusReference, ptName, prName string, numberOfRuns int) []string {
	if customRunNames := getRunNamesFromChildRefs(childRefs, ptName); customRunNames != nil {
		return customRunNames
	}
	return getNewRunNames(ptName, prName, numberOfRuns)
}

// getRunNamesFromChildRefs returns the names of CustomRuns defined in childRefs that are associated with the named Pipeline Task.
func getRunNamesFromChildRefs(childRefs []v1.ChildStatusReference, ptName string) []string {
	var runNames []string
	for _, cr := range childRefs {
		if cr.PipelineTaskName == ptName {
			if cr.Kind == pipeline.CustomRunControllerName {
				runNames = append(runNames, cr.Name)
			}
		}
	}
	return runNames
}
