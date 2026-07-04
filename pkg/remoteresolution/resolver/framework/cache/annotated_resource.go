/*
Copyright 2025 The Tekton Authors

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

package cache

import (
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	resolutionframework "github.com/tektoncd/pipeline/pkg/resolution/resolver/framework"
)

const (
	// CacheAnnotationKey is the annotation key indicating if a resource was cached
	CacheAnnotationKey = "resolution.tekton.dev/cached"
	// CacheTimestampKey is the annotation key for when the resource was cached
	CacheTimestampKey = "resolution.tekton.dev/cache-timestamp"
	// CacheResolverTypeKey is the annotation key for the resolver type that cached it
	CacheResolverTypeKey = "resolution.tekton.dev/cache-resolver-type"
	// CacheOperationKey is the annotation key for the cache operation type
	CacheOperationKey = "resolution.tekton.dev/cache-operation"
	// CacheValueTrue is the value used for cache annotations
	CacheValueTrue = "true"
	// CacheOperationStore is the value for cache store operations
	CacheOperationStore = "store"
	// CacheOperationRetrieve is the value for cache retrieve operations
	CacheOperationRetrieve = "retrieve"
)

// annotatedResource wraps a ResolvedResource with cache annotations
type annotatedResource struct {
	resource    resolutionframework.ResolvedResource
	annotations map[string]string
}

func newAnnotatedResource(
	resource resolutionframework.ResolvedResource,
	resolverType,
	operation string,
	timestamp string,
) *annotatedResource {
	// Create a new map to avoid concurrent map writes when the same resource
	// is being annotated from multiple goroutines
	existingAnnotations := resource.Annotations()
	annotations := make(map[string]string)

	for k, v := range existingAnnotations {
		annotations[k] = v
	}

	annotations[CacheAnnotationKey] = CacheValueTrue
	annotations[CacheTimestampKey] = timestamp
	annotations[CacheResolverTypeKey] = resolverType
	annotations[CacheOperationKey] = operation

	return &annotatedResource{
		resource:    resource,
		annotations: annotations,
	}
}

// Data returns the bytes of the resource
func (a *annotatedResource) Data() []byte {
	return a.resource.Data()
}

// Annotations returns the annotations with cache metadata
func (a *annotatedResource) Annotations() map[string]string {
	return a.annotations
}

// RefSource returns the source reference of the remote data
func (a *annotatedResource) RefSource() *v1.RefSource {
	return a.resource.RefSource()
}
