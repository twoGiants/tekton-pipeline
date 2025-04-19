/*
Copyright 2023 The Tekton Authors

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

package v1_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/config"
	cfgtesting "github.com/tektoncd/pipeline/pkg/apis/config/testing"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/test/diff"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

func enableConciseResolverSyntax(ctx context.Context) context.Context {
	return config.ToContext(ctx, &config.Config{
		FeatureFlags: &config.FeatureFlags{
			EnableConciseResolverSyntax: true,
			EnableAPIFields:             config.BetaAPIFields,
		},
	})
}

func TestRef_Valid(t *testing.T) {
	tests := []struct {
		name string
		ref  *v1.Ref
		wc   func(context.Context) context.Context
	}{{
		name: "nil ref",
	}, {
		name: "simple ref",
		ref:  &v1.Ref{Name: "refname"},
	}, {
		name: "ref name - concise syntax",
		ref:  &v1.Ref{Name: "foo://baz:ver", ResolverRef: v1.ResolverRef{Resolver: "git"}},
		wc:   enableConciseResolverSyntax,
	}, {
		name: "beta feature: valid resolver",
		ref:  &v1.Ref{ResolverRef: v1.ResolverRef{Resolver: "git"}},
		wc:   cfgtesting.EnableBetaAPIFields,
	}, {
		name: "beta feature: valid resolver with alpha flag",
		ref:  &v1.Ref{ResolverRef: v1.ResolverRef{Resolver: "git"}},
		wc:   cfgtesting.EnableAlphaAPIFields,
	}, {
		name: "beta feature: valid resolver with params",
		ref: &v1.Ref{ResolverRef: v1.ResolverRef{Resolver: "git", Params: v1.Params{{
			Name: "repo",
			Value: v1.ParamValue{
				Type:      v1.ParamTypeString,
				StringVal: "https://github.com/tektoncd/pipeline.git",
			},
		}, {
			Name: "branch",
			Value: v1.ParamValue{
				Type:      v1.ParamTypeString,
				StringVal: "baz",
			},
		}}}},
	}}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := context.Background()
			if ts.wc != nil {
				ctx = ts.wc(ctx)
			}
			if err := ts.ref.Validate(ctx); err != nil {
				t.Errorf("Ref.Validate() error = %v", err)
			}
		})
	}
}

func TestRef_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		ref     *v1.Ref
		wantErr *apis.FieldError
		wc      func(context.Context) context.Context
	}{{
		name:    "missing ref name",
		ref:     &v1.Ref{},
		wantErr: apis.ErrMissingField("name"),
	}, {
		name: "ref params disallowed without resolver",
		ref: &v1.Ref{
			ResolverRef: v1.ResolverRef{
				Params: v1.Params{},
			},
		},
		wantErr: apis.ErrMissingField("resolver"),
	}, {
		name: "ref with resolver and k8s style name",
		ref: &v1.Ref{
			Name: "foo",
			ResolverRef: v1.ResolverRef{
				Resolver: "git",
			},
		},
		wantErr: apis.ErrInvalidValue(`invalid URI for request`, "name"),
		wc:      enableConciseResolverSyntax,
	}, {
		name: "ref with url-like name without resolver",
		ref: &v1.Ref{
			Name: "https://foo.com/bar",
		},
		wantErr: apis.ErrMissingField("resolver"),
		wc:      enableConciseResolverSyntax,
	}, {
		name: "ref params disallowed in conjunction with pipelineref name",
		ref: &v1.Ref{
			Name: "https://foo/bar",
			ResolverRef: v1.ResolverRef{
				Resolver: "git",
				Params:   v1.Params{{Name: "foo", Value: v1.ParamValue{StringVal: "bar"}}},
			},
		},
		wantErr: apis.ErrMultipleOneOf("name", "params"),
		wc:      enableConciseResolverSyntax,
	}, {
		name: "ref with url-like name without enable-concise-resolver-syntax",
		ref:  &v1.Ref{Name: "https://foo.com/bar"},
		wantErr: apis.ErrMissingField("resolver").Also(&apis.FieldError{
			Message: `feature flag enable-concise-resolver-syntax should be set to true to use concise resolver syntax`,
		}),
	}, {
		name: "ref without enable-concise-resolver-syntax",
		ref:  &v1.Ref{Name: "https://foo.com/bar", ResolverRef: v1.ResolverRef{Resolver: "git"}},
		wantErr: &apis.FieldError{
			Message: `feature flag enable-concise-resolver-syntax should be set to true to use concise resolver syntax`,
		},
	}, {
		name: "invalid ref name",
		ref:  &v1.Ref{Name: "_foo"},
		wantErr: &apis.FieldError{
			Message: `invalid value: name part must consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')`,
			Paths:   []string{"name"},
		},
	}}
	for _, ts := range tests {
		t.Run(ts.name, func(t *testing.T) {
			ctx := context.Background()
			if ts.wc != nil {
				ctx = ts.wc(ctx)
			}
			err := ts.ref.Validate(ctx)
			if d := cmp.Diff(ts.wantErr.Error(), err.Error()); d != "" {
				t.Error(diff.PrintWantGot(d))
			}
		})
	}
}

func TestStepValidateSuccessWithArtifactsRefFlagEnabled(t *testing.T) {
	tests := []struct {
		name string
		Step v1.Step
	}{
		{
			name: "reference step artifacts in Env",
			Step: v1.Step{
				Image: "busybox",
				Env:   []corev1.EnvVar{{Name: "AAA", Value: "$(steps.aaa.outputs.image)"}},
			},
		},
		{
			name: "reference step artifacts path in Env",
			Step: v1.Step{
				Image: "busybox",
				Env:   []corev1.EnvVar{{Name: "AAA", Value: "$(step.artifacts.path)"}},
			},
		},
		{
			name: "reference step artifacts in Script",
			Step: v1.Step{
				Image:  "busybox",
				Script: "echo $(steps.aaa.inputs.bbb)",
			},
		},
		{
			name: "reference step artifacts path in Script",
			Step: v1.Step{
				Image:  "busybox",
				Script: "echo 123 >> $(step.artifacts.path)",
			},
		},
		{
			name: "reference step artifacts in Command",
			Step: v1.Step{
				Image:   "busybox",
				Command: []string{"echo", "$(steps.aaa.outputs.bbbb)"},
			},
		},
		{
			name: "reference step artifacts path in Command",
			Step: v1.Step{
				Image:   "busybox",
				Command: []string{"echo", "$(step.artifacts.path)"},
			},
		},
		{
			name: "reference step artifacts in Args",
			Step: v1.Step{
				Image: "busybox",
				Args:  []string{"echo", "$(steps.aaa.outputs.bbbb)"},
			},
		},
		{
			name: "reference step artifacts path in Args",
			Step: v1.Step{
				Image: "busybox",
				Args:  []string{"echo", "$(step.artifacts.path)"},
			},
		},
	}
	for _, st := range tests {
		t.Run(st.name, func(t *testing.T) {
			ctx := config.ToContext(context.Background(), &config.Config{
				FeatureFlags: &config.FeatureFlags{
					EnableStepActions: true,
					EnableArtifacts:   true,
				},
			})
			ctx = apis.WithinCreate(ctx)
			err := st.Step.Validate(ctx)
			if err != nil {
				t.Fatalf("Expected no errors, got err for %v", err)
			}
		})
	}
}

func TestStepValidateErrorWithArtifactsRefFlagNotEnabled(t *testing.T) {
	tests := []struct {
		name          string
		Step          v1.Step
		expectedError apis.FieldError
	}{
		{
			name: "Cannot reference step artifacts in Env without setting enable-artifacts to true",
			Step: v1.Step{
				Env: []corev1.EnvVar{{Name: "AAA", Value: "$(steps.aaa.outputs.image)"}},
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts path in Env without setting enable-artifacts to true",
			Step: v1.Step{
				Env: []corev1.EnvVar{{Name: "AAA", Value: "$(step.artifacts.path)"}},
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts in Script without setting enable-artifacts to true",
			Step: v1.Step{
				Script: "echo $(steps.aaa.inputs.bbb)",
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts path in Script without setting enable-artifacts to true",
			Step: v1.Step{
				Script: "echo 123 >> $(step.artifacts.path)",
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts in Command without setting enable-artifacts to true",
			Step: v1.Step{
				Command: []string{"echo", "$(steps.aaa.outputs.bbbb)"},
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts path in Command without setting enable-artifacts to true",
			Step: v1.Step{
				Command: []string{"echo", "$(step.artifacts.path)"},
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts in Args without setting enable-artifacts to true",
			Step: v1.Step{
				Args: []string{"echo", "$(steps.aaa.outputs.bbbb)"},
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
		{
			name: "Cannot reference step artifacts path in Args without setting enable-artifacts to true",
			Step: v1.Step{
				Args: []string{"echo", "$(step.artifacts.path)"},
			},
			expectedError: apis.FieldError{
				Message: fmt.Sprintf("feature flag %s should be set to true to use artifacts feature.", config.EnableArtifacts),
			},
		},
	}
	for _, st := range tests {
		t.Run(st.name, func(t *testing.T) {
			ctx := config.ToContext(context.Background(), &config.Config{
				FeatureFlags: &config.FeatureFlags{
					EnableStepActions: true,
				},
			})
			ctx = apis.WithinCreate(ctx)
			err := st.Step.Validate(ctx)
			if err == nil {
				t.Fatalf("Expected an error, got nothing for %v", st.Step)
			}
			if d := cmp.Diff(st.expectedError.Error(), err.Error(), cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
				t.Errorf("Step.Validate() errors diff %s", diff.PrintWantGot(d))
			}
		})
	}
}
