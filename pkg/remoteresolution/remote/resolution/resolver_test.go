/*
Copyright 2024 The Tekton Authors
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

package resolution

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	resv1beta1 "github.com/tektoncd/pipeline/pkg/apis/resolution/v1beta1"

	"github.com/tektoncd/pipeline/pkg/remote"
	remoteresource "github.com/tektoncd/pipeline/pkg/remoteresolution/resource"
	"github.com/tektoncd/pipeline/pkg/resolution/common"
	"github.com/tektoncd/pipeline/pkg/resolution/resource"
	"github.com/tektoncd/pipeline/test/diff"
	testremoteresolution "github.com/tektoncd/pipeline/test/remoteresolution"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/kmeta"

	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned/scheme"
	testresolution "github.com/tektoncd/pipeline/test/resolution"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var pipelineBytes = []byte(`
kind: Pipeline
apiVersion: tekton.dev/v1beta1
metadata:
  name: foo
spec:
  tasks:
  - name: task1
    taskSpec:
      steps:
      - name: step1
        image: docker.io/library/ubuntu
        script: |
          echo "hello world!"
`)

func TestGet_Successful(t *testing.T) {
	for _, tc := range []struct {
		resolvedData        []byte
		resolvedAnnotations map[string]string
	}{{
		resolvedData:        pipelineBytes,
		resolvedAnnotations: nil,
	}} {
		ctx := t.Context()
		owner := &v1beta1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
			},
		}
		resolved := &testremoteresolution.ResolvedResource{
			ResolvedData:        tc.resolvedData,
			ResolvedAnnotations: tc.resolvedAnnotations,
		}
		requester := &testremoteresolution.Requester{
			SubmitErr:        nil,
			ResolvedResource: resolved,
		}
		resolver := NewResolver(requester, owner, "git", remoteresource.ResolverPayload{})
		if _, _, err := resolver.Get(ctx, "foo", "bar"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestGet_Errors(t *testing.T) {
	genericError := errors.New("uh oh something bad happened")
	notARuntimeObject := &testremoteresolution.ResolvedResource{
		ResolvedData:        []byte(">:)"),
		ResolvedAnnotations: nil,
	}
	invalidDataResource := &testremoteresolution.ResolvedResource{
		DataErr:             errors.New("data access error"),
		ResolvedAnnotations: nil,
	}
	for _, tc := range []struct {
		submitErr        error
		expectedGetErr   error
		resolvedResource remoteresource.ResolvedResource
	}{{
		submitErr:        common.ErrRequestInProgress,
		expectedGetErr:   remote.ErrRequestInProgress,
		resolvedResource: nil,
	}, {
		submitErr:        nil,
		expectedGetErr:   ErrNilResource,
		resolvedResource: nil,
	}, {
		submitErr:        genericError,
		expectedGetErr:   genericError,
		resolvedResource: nil,
	}, {
		submitErr:        nil,
		expectedGetErr:   &InvalidRuntimeObjectError{},
		resolvedResource: notARuntimeObject,
	}, {
		submitErr:        nil,
		expectedGetErr:   &DataAccessError{},
		resolvedResource: invalidDataResource,
	}} {
		ctx := t.Context()
		owner := &v1beta1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
			},
		}
		requester := &testremoteresolution.Requester{
			SubmitErr:        tc.submitErr,
			ResolvedResource: tc.resolvedResource,
		}
		resolver := NewResolver(requester, owner, "git", remoteresource.ResolverPayload{})
		obj, refSource, err := resolver.Get(ctx, "foo", "bar")
		if obj != nil {
			t.Errorf("received unexpected resolved resource")
		}
		if refSource != nil {
			t.Errorf("expected refSource is nil, but received %v", refSource)
		}
		if !errors.Is(err, tc.expectedGetErr) {
			t.Fatalf("expected %v received %v", tc.expectedGetErr, err)
		}
	}
}

func TestRemoteResolutionBuildRequest(t *testing.T) {
	for _, tc := range []struct {
		name            string
		targetName      string
		targetNamespace string
		url             string
	}{{
		name: "just owner",
	}, {
		name:            "with target name and namespace",
		targetName:      "some-object",
		targetNamespace: "some-ns",
	}, {
		name:            "with target name, namespace, and url",
		targetName:      "some-object",
		targetNamespace: "some-ns",
		url:             "scheme://value",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			owner := &v1beta1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "bar",
				},
			}

			rr := &remoteresource.ResolverPayload{Name: tc.targetName, Namespace: tc.targetNamespace}
			rr.ResolutionSpec = &resv1beta1.ResolutionRequestSpec{URL: tc.url}
			req, err := buildRequest("git", owner, rr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d := cmp.Diff(*kmeta.NewControllerRef(owner), req.OwnerRef()); d != "" {
				t.Errorf("expected matching owner ref but got %s", diff.PrintWantGot(d))
			}
			reqNameBase := owner.Namespace + "/" + owner.Name
			if tc.targetName != "" {
				reqNameBase = tc.targetNamespace + "/" + tc.targetName
			}
			expectedReqName, err := resource.GenerateDeterministicNameFromSpec("git", reqNameBase, rr.ResolutionSpec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if expectedReqName != req.ResolverPayload().Name {
				t.Errorf("expected request name %s, but was %s", expectedReqName, req.ResolverPayload().Name)
			}
		})
	}
}

func TestResolvedRequest_AnnotationPropagation(t *testing.T) {
	var pipelineBytes = []byte(`
	kind: Pipeline
	apiVersion: tekton.dev/v1beta1
	metadata:
		name: foo
	spec:
		tasks:
		- name: task1
			taskSpec:
				steps:
				- name: step1
					image: docker.io/library/ubuntu
					script: |
						echo "hello world!"
	`)

	var taskBytes = []byte(`
	kind: Task
	apiVersion: tekton.dev/v1
	metadata:
		name: foo
	spec:
		steps:
		- name: step1
			image: ubuntu
			script: echo "hello"
	`)

	decoder := serializer.
		NewCodecFactory(scheme.Scheme, serializer.EnableStrict).
		UniversalDeserializer()

	for _, tc := range []struct {
		name                string
		resourceBytes       []byte
		resolverAnnotations map[string]string
		expectedAnnotations map[string]string
	}{
		{
			name:          "Task with cache annotations",
			resourceBytes: taskBytes,
			resolverAnnotations: map[string]string{
				"resolution.tekton.dev/cached":              "true",
				"resolution.tekton.dev/cache-timestamp":     "2025-01-01T00:00:00Z",
				"resolution.tekton.dev/cache-operation":     "store",
				"resolution.tekton.dev/cache-resolver-type": "bundles",
			},
			expectedAnnotations: map[string]string{
				"resolution.tekton.dev/cached":              "true",
				"resolution.tekton.dev/cache-timestamp":     "2025-01-01T00:00:00Z",
				"resolution.tekton.dev/cache-operation":     "store",
				"resolution.tekton.dev/cache-resolver-type": "bundles",
			},
		},
		{
			name:          "Pipeline with cache annotations",
			resourceBytes: pipelineBytes,
			resolverAnnotations: map[string]string{
				"resolution.tekton.dev/cached":              "true",
				"resolution.tekton.dev/cache-timestamp":     "2025-01-01T00:00:00Z",
				"resolution.tekton.dev/cache-operation":     "retrieve",
				"resolution.tekton.dev/cache-resolver-type": "git",
			},
			expectedAnnotations: map[string]string{
				"resolution.tekton.dev/cached":              "true",
				"resolution.tekton.dev/cache-timestamp":     "2025-01-01T00:00:00Z",
				"resolution.tekton.dev/cache-operation":     "retrieve",
				"resolution.tekton.dev/cache-resolver-type": "git",
			},
		},
		{
			name:                "Pipeline without any annotations",
			resourceBytes:       pipelineBytes,
			resolverAnnotations: nil,
			expectedAnnotations: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolved := testresolution.NewResolvedResource(tc.resourceBytes, tc.resolverAnnotations, nil, nil)

			obj, _, err := ResolvedRequest(resolved, decoder, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			metaObj, ok := obj.(metav1.Object)
			if !ok {
				t.Fatal("expected object to implement metav1.Object")
			}

			if diff := cmp.Diff(tc.expectedAnnotations, metaObj.GetAnnotations()); diff != "" {
				t.Errorf("annotations mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// nonMetaObjectFake is a runtime.Object that doesn't implement metav1.Object
type nonMetaObjectFake struct{}

func (*nonMetaObjectFake) GetObjectKind() schema.ObjectKind { return nil }
func (*nonMetaObjectFake) DeepCopyObject() runtime.Object   { return nil }

// decoderFake is a mock decoder that returns a non-metav1.Object
type decoderFake struct{}

func (*decoderFake) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error) {
	return &nonMetaObjectFake{}, nil, nil
}

func TestResolvedRequest_UnsupportedObjectType(t *testing.T) {
	// GIVEN
	expectedErrMsg := "resolved resource type \"*resolution.nonMetaObjectFake\" does not support annotations"
	decoderFake := &decoderFake{}
	resolved := testresolution.NewResolvedResource([]byte("test"), map[string]string{
		"resolution.tekton.dev/cache": "true",
	}, nil, nil)

	// WHEN
	_, _, err := ResolvedRequest(resolved, decoderFake, nil)

	// THEN
	if !errors.Is(err, &UnsupportedObjectTypeError{}) {
		t.Fatalf("expected UnsupportedObjectTypeError, got %v", err)
	}

	if err.Error() != expectedErrMsg {
		t.Errorf("expected error %q, got %q", expectedErrMsg, err.Error())
	}
}
