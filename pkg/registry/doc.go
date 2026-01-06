/*
Copyright 2014 The Kubernetes Authors.

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

// Package registry implements the storage and system logic for the core of the api server.
package registry

// The package contains the registry of the storage handlers for the built-in resource types.
// The list of storage providers are installed into the API server by `InstallAPIs` in `pkg/controlplane/instance.go`
// with storage handlers for REST paths eventually installed through `Install` in `staging/src/k8s.io/apiserver/pkg/endpoints/installer.go`.
// The storage handlers implement the REST interface defined in `staging/src/k8s.io/apiserver/pkg/registry/rest/rest.go`.
// The storage handlers are then converted into HTTP handlers by the functions in `staging/src/k8s.io/apiserver/pkg/endpoints/handlers/`.
//
// Each API group (e.g., core, apps, batch) provides a NewRESTStorage function that initializes
// the storage for its resources and registers them in a VersionedResourcesStorageMap.
//
// Subresources are registered in the storage map by appending a slash and the subresource name
// to the main resource name. For example, pods/status, pods/log, and services/proxy are registered
// as separate entries in the map.
//
// Example registration pattern (from pkg/registry/core/rest/storage_core.go):
//
//	if resource := "pods"; apiResourceConfigSource.ResourceEnabled(corev1.SchemeGroupVersion.WithResource(resource)) {
//		storage[resource] = podStorage.Pod
//		storage[resource+"/status"] = podStorage.Status
//		storage[resource+"/log"] = podStorage.Log
//		// ... other subresources
//	}