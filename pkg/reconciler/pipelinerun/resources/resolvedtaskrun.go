package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/reconciler/taskrun/resources"
	"github.com/tektoncd/pipeline/pkg/remote"
	resolutioncommon "github.com/tektoncd/pipeline/pkg/resolution/common"
	"github.com/tektoncd/pipeline/pkg/resolution/resource"
	"github.com/tektoncd/pipeline/pkg/trustedresources"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/controller"
)

type ResolvedTaskRun struct {
	TaskRunNames []string
	TaskRuns     []*v1.TaskRun
	ResolvedTask *resources.ResolvedTask

	pipelineTask    *v1.PipelineTask
	pipelineRunName string
	numCombinations int
	getTask         resources.GetTask
	getTaskRun      resources.GetTaskRun
	childReferences []v1.ChildStatusReference
}

func NewResolvedTaskRun(
	pipelineTask *v1.PipelineTask,
	pipelineRun v1.PipelineRun,
	getTask resources.GetTask,
	getTaskRun resources.GetTaskRun,
) *ResolvedTaskRun {
	return &ResolvedTaskRun{
		pipelineTask:    pipelineTask,
		pipelineRunName: pipelineRun.Name,
		numCombinations: matrixCombinations(pipelineTask),
		getTask:         getTask,
		getTaskRun:      getTaskRun,
		childReferences: pipelineRun.Status.ChildReferences,
	}
}

func (rtr *ResolvedTaskRun) UpdateConditionFromVerificationResult(pr *v1.PipelineRun) error {
	if rtr.ResolvedTask == nil || rtr.ResolvedTask.VerificationResult != nil {
		return nil
	}

	cond, err := rtr.conditionFromVerificationResult(pr)
	pr.Status.SetCondition(cond)
	if err != nil {
		pr.Status.MarkFailed(
			v1.PipelineRunReasonResourceVerificationFailed.String(),
			err.Error())
		return controller.NewPermanentError(err)
	}

	return nil
}

// TODO: duplicate, extract to common.go
func (rtr *ResolvedTaskRun) conditionFromVerificationResult(
	pr *v1.PipelineRun,
) (*apis.Condition, error) {
	var condition *apis.Condition
	var err error
	switch rtr.ResolvedTask.VerificationResult.VerificationResultType {
	case trustedresources.VerificationError:
		err = fmt.Errorf(
			"pipelineRun %s/%s referred resource %s failed signature verification: %w",
			pr.Namespace,
			pr.Name,
			rtr.pipelineTask.Name,
			rtr.ResolvedTask.VerificationResult.Err,
		)
		condition = &apis.Condition{
			Type:    trustedresources.ConditionTrustedResourcesVerified,
			Status:  corev1.ConditionFalse,
			Message: err.Error(),
		}
	case trustedresources.VerificationWarn:
		condition = &apis.Condition{
			Type:    trustedresources.ConditionTrustedResourcesVerified,
			Status:  corev1.ConditionFalse,
			Message: rtr.ResolvedTask.VerificationResult.Err.Error(),
		}
	case trustedresources.VerificationPass:
		condition = &apis.Condition{
			Type:   trustedresources.ConditionTrustedResourcesVerified,
			Status: corev1.ConditionTrue,
		}
	case trustedresources.VerificationSkip:
		// do nothing
	}
	return condition, err
}

func (rtr *ResolvedTaskRun) ResolveResource(ctx context.Context) error {
	rtr.TaskRunNames = GetNamesOfTaskRuns(
		rtr.childReferences,
		rtr.pipelineTask.Name,
		rtr.pipelineRunName,
		rtr.numCombinations,
	)
	for _, taskRunName := range rtr.TaskRunNames {
		if err := rtr.setTaskRunsAndResolvedTask(
			ctx,
			taskRunName,
		); err != nil {
			return err
		}
	}

	return nil
}

// GetNamesOfTaskRuns should return unique names for `TaskRuns` if one has not already been defined, and the existing one otherwise.
func GetNamesOfTaskRuns(childRefs []v1.ChildStatusReference, ptName, prName string, numberOfTaskRuns int) []string {
	if taskRunNames := getTaskRunNamesFromChildRefs(childRefs, ptName); taskRunNames != nil {
		return taskRunNames
	}
	return getNewRunNames(ptName, prName, numberOfTaskRuns)
}

// getTaskRunNamesFromChildRefs returns the names of TaskRuns defined in childRefs that are associated with the named Pipeline Task.
func getTaskRunNamesFromChildRefs(childRefs []v1.ChildStatusReference, ptName string) []string {
	var taskRunNames []string
	for _, cr := range childRefs {
		if cr.Kind == pipeline.TaskRunControllerName && cr.PipelineTaskName == ptName {
			taskRunNames = append(taskRunNames, cr.Name)
		}
	}
	return taskRunNames
}

func (rtr *ResolvedTaskRun) setTaskRunsAndResolvedTask(
	ctx context.Context,
	taskRunName string,
) error {
	taskRun, err := rtr.getTaskRun(taskRunName)
	if err != nil {
		if !kerrors.IsNotFound(err) {
			return fmt.Errorf("error retrieving TaskRun %s: %w", taskRunName, err)
		}
	}
	if taskRun != nil {
		rtr.TaskRuns = append(rtr.TaskRuns, taskRun)
	}

	rt, err := rtr.resolveTask(ctx, taskRun)
	if err != nil {
		return err
	}
	rtr.ResolvedTask = rt
	return nil
}

func (rtr *ResolvedTaskRun) resolveTask(
	ctx context.Context,
	taskRun *v1.TaskRun,
) (*resources.ResolvedTask, error) {
	rt := &resources.ResolvedTask{}
	switch {
	case rtr.pipelineTask.TaskRef != nil:
		// If the TaskRun has already a stored TaskSpec in its status, use it as source of truth
		if taskRun != nil && taskRun.Status.TaskSpec != nil {
			rt.TaskSpec = taskRun.Status.TaskSpec
			rt.TaskName = rtr.pipelineTask.TaskRef.Name
		} else {
			// Following minimum status principle (TEP-0100), no need to propagate the RefSource about PipelineTask up to PipelineRun status.
			// Instead, the child TaskRun's status will be the place recording the RefSource of individual task.
			t, _, vr, err := rtr.getTask(ctx, rtr.pipelineTask.TaskRef.Name)
			switch {
			case errors.Is(err, remote.ErrRequestInProgress) || (err != nil && resolutioncommon.IsErrTransient(err)):
				return rt, err
			case err != nil:
				// some of the resolvers obtain the name from the parameters instead of from the TaskRef.Name field,
				// so we account for both locations when constructing the error
				name := rtr.pipelineTask.TaskRef.Name
				if len(strings.TrimSpace(name)) == 0 {
					name = resource.GenerateErrorLogString(string(rtr.pipelineTask.TaskRef.Resolver), rtr.pipelineTask.TaskRef.Params)
				}
				return rt, &TaskNotFoundError{
					Name: name,
					Msg:  err.Error(),
				}
			default:
				spec := t.Spec
				rt.TaskSpec = &spec
				rt.TaskName = t.Name
				rt.VerificationResult = vr
			}
		}
		rt.Kind = rtr.pipelineTask.TaskRef.Kind
	case rtr.pipelineTask.TaskSpec != nil:
		rt.TaskSpec = &rtr.pipelineTask.TaskSpec.TaskSpec
	default:
		// If the alpha feature is enabled, and the user has configured pipelineSpec or pipelineRef, it will enter here.
		// Currently, the controller is not yet adapted, and to avoid a panic, an error message is provided here.
		// TODO: Adjust the logic here once the feature is supported in the future.
		return nil, fmt.Errorf("Currently, Task %q does not support PipelineRef, please use PipelineSpec, TaskRef or TaskSpec instead", rtr.pipelineTask.Name)
	}
	rt.TaskSpec.SetDefaults(ctx)
	return rt, nil
}
