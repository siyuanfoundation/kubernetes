/*
Copyright 2026 The Kubernetes Authors.

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

// Package apis contains the internal "hub" types for the Kubernetes API.
//
// These internal types are not serializable and do not have JSON or protobuf tags.
// Their purpose is to allow the server to have a single code path that deals with
// one Go type, regardless of the version the client is talking in or the version
// stored in etcd.
//
// When a client sends a versioned object, or when a versioned object is read from
// etcd, it is converted to this internal hub type for processing.
package apis
