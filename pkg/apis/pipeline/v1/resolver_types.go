/*
Copyright 2022 The Tekton Authors

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

package v1

// ResolverName is the name of a resolver from which a resource can be
// requested.
type ResolverName string

// ResolverRef can be used to refer to a Pipeline or Task in a remote
// location like a git repo. This feature is in beta and these fields
// are only available when the beta feature gate is enabled.
type ResolverRef struct {
	// Resolver is the name of the resolver that should perform
	// resolution of the referenced Tekton resource, such as "git".
	// +optional
	Resolver ResolverName `json:"resolver,omitempty"`
	// Params contains the parameters used to identify the
	// referenced Tekton resource. Example entries might include
	// "repo" or "path" but the set of params ultimately depends on
	// the chosen resolver.
	// +optional
	Params Params `json:"params,omitempty"`
}
