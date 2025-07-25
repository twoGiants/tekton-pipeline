/*
Copyright 2019 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pipelinerunmetrics

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/config"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
	"go.uber.org/zap"
	"golang.org/x/crypto/blake2b"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/labels"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/metrics"
)

const (
	runningPRLevelPipelinerun = "pipelinerun"
	runningPRLevelPipeline    = "pipeline"
	runningPRLevelNamespace   = "namespace"
	runningPRLevelCluster     = ""
)

var (
	pipelinerunTag = tag.MustNewKey("pipelinerun")
	pipelineTag    = tag.MustNewKey("pipeline")
	namespaceTag   = tag.MustNewKey("namespace")
	statusTag      = tag.MustNewKey("status")
	reasonTag      = tag.MustNewKey("reason")

	prDuration = stats.Float64(
		"pipelinerun_duration_seconds",
		"The pipelinerun execution time in seconds",
		stats.UnitDimensionless)
	prDurationView *view.View

	prTotal = stats.Float64("pipelinerun_total",
		"Number of pipelineruns",
		stats.UnitDimensionless)
	prTotalView *view.View

	runningPRs = stats.Float64("running_pipelineruns",
		"Number of pipelineruns executing currently",
		stats.UnitDimensionless)
	runningPRsView *view.View

	runningPRsWaitingOnPipelineResolution = stats.Float64("running_pipelineruns_waiting_on_pipeline_resolution",
		"Number of pipelineruns executing currently that are waiting on resolution requests for their pipeline references.",
		stats.UnitDimensionless)
	runningPRsWaitingOnPipelineResolutionView *view.View

	runningPRsWaitingOnTaskResolution = stats.Float64("running_pipelineruns_waiting_on_task_resolution",
		"Number of pipelineruns executing currently that are waiting on resolution requests for the task references of their taskrun children.",
		stats.UnitDimensionless)
	runningPRsWaitingOnTaskResolutionView *view.View
)

const (
	// ReasonCancelled indicates that a PipelineRun was cancelled.
	// Aliased for backwards compatibility; additional reasons should not be added here.
	ReasonCancelled = v1.PipelineRunReasonCancelled

	anonymous = "anonymous"
)

// Recorder holds keys for Tekton metrics
type Recorder struct {
	mutex       sync.Mutex
	initialized bool
	cfg         *config.Metrics

	insertTag func(pipeline,
		pipelinerun string) []tag.Mutator

	ReportingPeriod time.Duration

	hash string
}

// We cannot register the view multiple times, so NewRecorder lazily
// initializes this singleton and returns the same recorder across any
// subsequent invocations.
var (
	once           sync.Once
	r              *Recorder
	errRegistering error
)

// NewRecorder creates a new metrics recorder instance
// to log the PipelineRun related metrics
func NewRecorder(ctx context.Context) (*Recorder, error) {
	once.Do(func() {
		r = &Recorder{
			initialized: true,

			// Default to 30s intervals.
			ReportingPeriod: 30 * time.Second,
		}

		cfg := config.FromContextOrDefaults(ctx)
		r.cfg = cfg.Metrics
		errRegistering = viewRegister(cfg.Metrics)
		if errRegistering != nil {
			r.initialized = false
			return
		}
	})

	return r, errRegistering
}

func viewRegister(cfg *config.Metrics) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	var prunTag []tag.Key
	switch cfg.PipelinerunLevel {
	case config.PipelinerunLevelAtPipelinerun:
		prunTag = []tag.Key{pipelinerunTag, pipelineTag}
		r.insertTag = pipelinerunInsertTag
	case config.PipelinerunLevelAtPipeline:
		prunTag = []tag.Key{pipelineTag}
		r.insertTag = pipelineInsertTag
	case config.PipelinerunLevelAtNS:
		prunTag = []tag.Key{}
		r.insertTag = nilInsertTag
	default:
		return errors.New("invalid config for PipelinerunLevel: " + cfg.PipelinerunLevel)
	}

	var runningPRTag []tag.Key
	switch cfg.RunningPipelinerunLevel {
	case config.PipelinerunLevelAtPipelinerun:
		runningPRTag = []tag.Key{pipelinerunTag, pipelineTag, namespaceTag}
	case config.PipelinerunLevelAtPipeline:
		runningPRTag = []tag.Key{pipelineTag, namespaceTag}
	case config.PipelinerunLevelAtNS:
		runningPRTag = []tag.Key{namespaceTag}
	default:
		runningPRTag = []tag.Key{}
	}

	distribution := view.Distribution(10, 30, 60, 300, 900, 1800, 3600, 5400, 10800, 21600, 43200, 86400)

	if cfg.PipelinerunLevel == config.PipelinerunLevelAtPipelinerun {
		distribution = view.LastValue()
	} else {
		switch cfg.DurationPipelinerunType {
		case config.DurationTaskrunTypeHistogram:
		case config.DurationTaskrunTypeLastValue:
			distribution = view.LastValue()
		default:
			return errors.New("invalid config for DurationTaskrunType: " + cfg.DurationTaskrunType)
		}
	}

	if cfg.CountWithReason {
		prunTag = append(prunTag, reasonTag)
	}

	prDurationView = &view.View{
		Description: prDuration.Description(),
		Measure:     prDuration,
		Aggregation: distribution,
		TagKeys:     append([]tag.Key{statusTag, namespaceTag}, prunTag...),
	}

	prTotalView = &view.View{
		Description: prTotal.Description(),
		Measure:     prTotal,
		Aggregation: view.Count(),
		TagKeys:     []tag.Key{statusTag},
	}

	runningPRsView = &view.View{
		Description: runningPRs.Description(),
		Measure:     runningPRs,
		Aggregation: view.LastValue(),
		TagKeys:     runningPRTag,
	}

	runningPRsWaitingOnPipelineResolutionView = &view.View{
		Description: runningPRsWaitingOnPipelineResolution.Description(),
		Measure:     runningPRsWaitingOnPipelineResolution,
		Aggregation: view.LastValue(),
	}

	runningPRsWaitingOnTaskResolutionView = &view.View{
		Description: runningPRsWaitingOnTaskResolution.Description(),
		Measure:     runningPRsWaitingOnTaskResolution,
		Aggregation: view.LastValue(),
	}

	return view.Register(
		prDurationView,
		prTotalView,
		runningPRsView,
		runningPRsWaitingOnPipelineResolutionView,
		runningPRsWaitingOnTaskResolutionView,
	)
}

func viewUnregister() {
	view.Unregister(prDurationView,
		prTotalView,
		runningPRsView,
		runningPRsWaitingOnPipelineResolutionView,
		runningPRsWaitingOnTaskResolutionView)
}

// OnStore returns a function that checks if metrics are configured for a config.Store, and registers it if so
func OnStore(logger *zap.SugaredLogger, r *Recorder) func(name string,
	value interface{}) {
	return func(name string, value interface{}) {
		if name == config.GetMetricsConfigName() {
			cfg, ok := value.(*config.Metrics)
			if !ok {
				logger.Error("Failed to do type insertion for extracting metrics config")
				return
			}
			updated := r.updateConfig(cfg)
			if !updated {
				return
			}
			// Update metrics according to configuration
			viewUnregister()
			err := viewRegister(cfg)
			if err != nil {
				logger.Errorf("Failed to register View %v ", err)
				return
			}
		}
	}
}

func pipelinerunInsertTag(pipeline, pipelinerun string) []tag.Mutator {
	return []tag.Mutator{
		tag.Insert(pipelineTag, pipeline),
		tag.Insert(pipelinerunTag, pipelinerun),
	}
}

func pipelineInsertTag(pipeline, pipelinerun string) []tag.Mutator {
	return []tag.Mutator{tag.Insert(pipelineTag, pipeline)}
}

func nilInsertTag(task, taskrun string) []tag.Mutator {
	return []tag.Mutator{}
}

func getPipelineTagName(pr *v1.PipelineRun) string {
	pipelineName := anonymous
	switch {
	case pr.Spec.PipelineRef != nil && pr.Spec.PipelineRef.Name != "":
		pipelineName = pr.Spec.PipelineRef.Name
	case pr.Spec.PipelineSpec != nil:
	default:
		if len(pr.Labels) > 0 {
			pipelineLabel, hasPipelineLabel := pr.Labels[pipeline.PipelineLabelKey]
			if hasPipelineLabel && len(pipelineLabel) > 0 {
				pipelineName = pipelineLabel
			}
		}
	}

	return pipelineName
}

func (r *Recorder) updateConfig(cfg *config.Metrics) bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	var hash string
	if cfg != nil {
		s := fmt.Sprintf("%v", *cfg)
		sum := blake2b.Sum256([]byte(s))
		hash = hex.EncodeToString(sum[:])
	}

	if r.hash == hash {
		return false
	}

	r.cfg = cfg
	r.hash = hash

	return true
}

// DurationAndCount logs the duration of PipelineRun execution and
// count for number of PipelineRuns succeed or failed
// returns an error if it fails to log the metrics
func (r *Recorder) DurationAndCount(pr *v1.PipelineRun, beforeCondition *apis.Condition) error {
	if !r.initialized {
		return fmt.Errorf("ignoring the metrics recording for %s , failed to initialize the metrics recorder", pr.Name)
	}

	afterCondition := pr.Status.GetCondition(apis.ConditionSucceeded)
	// To avoid recount
	if equality.Semantic.DeepEqual(beforeCondition, afterCondition) {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	duration := time.Duration(0)
	if pr.Status.StartTime != nil {
		duration = time.Since(pr.Status.StartTime.Time)
		if pr.Status.CompletionTime != nil {
			duration = pr.Status.CompletionTime.Sub(pr.Status.StartTime.Time)
		}
	}

	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	status := "success"
	if cond.Status == corev1.ConditionFalse {
		status = "failed"
		if cond.Reason == v1.PipelineRunReasonCancelled.String() {
			status = "cancelled"
		}
	}
	reason := cond.Reason

	pipelineName := getPipelineTagName(pr)

	ctx, err := tag.New(
		context.Background(),
		append([]tag.Mutator{
			tag.Insert(namespaceTag, pr.Namespace),
			tag.Insert(statusTag, status), tag.Insert(reasonTag, reason),
		}, r.insertTag(pipelineName, pr.Name)...)...)
	if err != nil {
		return err
	}

	metrics.Record(ctx, prDuration.M(duration.Seconds()))
	metrics.Record(ctx, prTotal.M(1))

	return nil
}

// RunningPipelineRuns logs the number of PipelineRuns running right now
// returns an error if it fails to log the metrics
func (r *Recorder) RunningPipelineRuns(lister listers.PipelineRunLister) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if !r.initialized {
		return errors.New("ignoring the metrics recording, failed to initialize the metrics recorder")
	}

	prs, err := lister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("failed to list pipelineruns while generating metrics : %w", err)
	}

	var runningPipelineRuns int
	var trsWaitResolvingTaskRef int
	var prsWaitResolvingPipelineRef int
	countMap := map[string]int{}

	for _, pr := range prs {
		pipelineName := getPipelineTagName(pr)
		pipelineRunKey := ""
		mutators := []tag.Mutator{
			tag.Insert(namespaceTag, pr.Namespace),
			tag.Insert(pipelineTag, pipelineName),
			tag.Insert(pipelinerunTag, pr.Name),
		}
		if r.cfg != nil {
			switch r.cfg.RunningPipelinerunLevel {
			case runningPRLevelPipelinerun:
				pipelineRunKey = pipelineRunKey + "#" + pr.Name
				fallthrough
			case runningPRLevelPipeline:
				pipelineRunKey = pipelineRunKey + "#" + pipelineName
				fallthrough
			case runningPRLevelNamespace:
				pipelineRunKey = pipelineRunKey + "#" + pr.Namespace
			case runningPRLevelCluster:
			default:
				return fmt.Errorf("RunningPipelineRunLevel value \"%s\" is not valid ", r.cfg.RunningPipelinerunLevel)
			}
		}
		ctx_, err_ := tag.New(context.Background(), mutators...)
		if err_ != nil {
			return err
		}
		if !pr.IsDone() {
			countMap[pipelineRunKey]++
			metrics.Record(ctx_, runningPRs.M(float64(countMap[pipelineRunKey])))
			runningPipelineRuns++
			succeedCondition := pr.Status.GetCondition(apis.ConditionSucceeded)
			if succeedCondition != nil && succeedCondition.Status == corev1.ConditionUnknown {
				switch succeedCondition.Reason {
				case v1.TaskRunReasonResolvingTaskRef:
					trsWaitResolvingTaskRef++
				case v1.PipelineRunReasonResolvingPipelineRef.String():
					prsWaitResolvingPipelineRef++
				}
			}
		} else {
			// In case there are no running PipelineRuns for the pipelineRunKey, set the metric value to 0 to ensure
			//  the metric is set for the key.
			if _, exists := countMap[pipelineRunKey]; !exists {
				countMap[pipelineRunKey] = 0
				metrics.Record(ctx_, runningPRs.M(0))
			}
		}
	}

	ctx, err := tag.New(context.Background())
	if err != nil {
		return err
	}
	metrics.Record(ctx, runningPRsWaitingOnPipelineResolution.M(float64(prsWaitResolvingPipelineRef)))
	metrics.Record(ctx, runningPRsWaitingOnTaskResolution.M(float64(trsWaitResolvingTaskRef)))
	metrics.Record(ctx, runningPRs.M(float64(runningPipelineRuns)))

	return nil
}

// ReportRunningPipelineRuns invokes RunningPipelineRuns on our configured PeriodSeconds
// until the context is cancelled.
func (r *Recorder) ReportRunningPipelineRuns(ctx context.Context, lister listers.PipelineRunLister) {
	logger := logging.FromContext(ctx)

	for {
		delay := time.NewTimer(r.ReportingPeriod)
		select {
		case <-ctx.Done():
			// When the context is cancelled, stop reporting.
			if !delay.Stop() {
				<-delay.C
			}
			return

		case <-delay.C:
			// Every 30s surface a metric for the number of running pipelines.
			if err := r.RunningPipelineRuns(lister); err != nil {
				logger.Warnf("Failed to log the metrics : %v", err)
			}
		}
	}
}
