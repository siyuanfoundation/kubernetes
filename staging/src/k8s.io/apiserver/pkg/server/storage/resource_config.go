/*
Copyright 2016 The Kubernetes Authors.

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

package storage

import (
	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/component-base/version"
	"k8s.io/klog/v2"
)

// APIResourceConfigSource is the interface to determine which groups and versions are enabled
type APIResourceConfigSource interface {
	ResourceEnabled(resource schema.GroupVersionResource) bool
	AnyResourceForGroupEnabled(group string) bool
	GetGroupVersionConfigs() map[schema.GroupVersion]bool
}

// GroupVersionRegistry provides access to registered group versions.
type GroupVersionRegistry interface {
	// IsGroupRegistered returns true if given group is registered.
	IsGroupRegistered(group string) bool
	// IsVersionRegistered returns true if given version is registered.
	IsVersionRegistered(v schema.GroupVersion) bool
	// PrioritizedVersionsAllGroups returns all registered group versions.
	PrioritizedVersionsAllGroups() []schema.GroupVersion
	// GroupVersionLifecycle returns the APILifecycle for the GroupVersion.
	GroupVersionLifecycle(gv schema.GroupVersion) schema.APILifecycle
	// ResourceLifecycle returns the APILifecycle for the GroupVersionResource.
	ResourceLifecycle(gvr schema.GroupVersionResource) schema.APILifecycle
}

var _ APIResourceConfigSource = &ResourceConfig{}

type ResourceConfig struct {
	GroupVersionConfigs  map[schema.GroupVersion]bool
	ResourceConfigs      map[schema.GroupVersionResource]bool
	compatibilityVersion semver.Version
	GroupVersionRegistry
}

func NewResourceConfig(compatibilityVersion string, registry GroupVersionRegistry) *ResourceConfig {
	if compatibilityVersion == "" {
		compatibilityVersion = version.Get().GitVersion
	}
	ver := version.MustParseVersion(compatibilityVersion)
	return &ResourceConfig{GroupVersionConfigs: map[schema.GroupVersion]bool{}, ResourceConfigs: map[schema.GroupVersionResource]bool{},
		compatibilityVersion: ver, GroupVersionRegistry: registry}
}

// DisableMatchingVersions disables all group/versions for which the matcher function returns true.
// This will remove any preferences previously set on individual resources.
func (o *ResourceConfig) DisableMatchingVersions(matcher func(gv schema.GroupVersion) bool) {
	for version := range o.GroupVersionConfigs {
		if matcher(version) {
			o.GroupVersionConfigs[version] = false
			o.removeMatchingResourcePreferences(resourceMatcherForVersion(version))
		}
	}
}

// EnableMatchingVersions enables all group/versions for which the matcher function returns true.
// This will remove any preferences previously set on individual resources.
func (o *ResourceConfig) EnableMatchingVersions(matcher func(gv schema.GroupVersion) bool) {
	for version := range o.GroupVersionConfigs {
		if matcher(version) && o.versionAvailable(version) {
			o.GroupVersionConfigs[version] = true
			o.removeMatchingResourcePreferences(resourceMatcherForVersion(version))
		}
	}
}

// resourceMatcherForVersion matches resources in the specified version
func resourceMatcherForVersion(gv schema.GroupVersion) func(gvr schema.GroupVersionResource) bool {
	return func(gvr schema.GroupVersionResource) bool {
		return gv == gvr.GroupVersion()
	}
}

// removeMatchingResourcePreferences removes individual resource preferences that match.  This is useful when an override of a version or level enablement should
// override the previously individual preferences.
func (o *ResourceConfig) removeMatchingResourcePreferences(matcher func(gvr schema.GroupVersionResource) bool) {
	keysToRemove := []schema.GroupVersionResource{}
	for k := range o.ResourceConfigs {
		if matcher(k) {
			keysToRemove = append(keysToRemove, k)
		}
	}
	for _, k := range keysToRemove {
		delete(o.ResourceConfigs, k)
	}
}

// DisableVersions disables the versions entirely.
// This will remove any preferences previously set on individual resources.
func (o *ResourceConfig) DisableVersions(versions ...schema.GroupVersion) {
	for _, version := range versions {
		o.GroupVersionConfigs[version] = false
		// a preference about a version takes priority over the previously set resources
		o.removeMatchingResourcePreferences(resourceMatcherForVersion(version))
	}
}

// EnableVersions enables all resources in a given groupVersion.
// This will remove any preferences previously set on individual resources.
func (o *ResourceConfig) EnableVersions(versions ...schema.GroupVersion) {
	for _, version := range versions {
		if o.versionAvailable(version) {
			o.GroupVersionConfigs[version] = true
		} else {
			o.GroupVersionConfigs[version] = false
			klog.V(1).Infof("version %s cannot be enabled due to its api lifecyle.", version.String())
		}
		// a preference about a version takes priority over the previously set resources
		o.removeMatchingResourcePreferences(resourceMatcherForVersion(version))
	}
}

// TODO this must be removed and we enable/disable individual resources.
func (o *ResourceConfig) versionEnabled(version schema.GroupVersion) bool {
	enabled, _ := o.GroupVersionConfigs[version]
	return enabled
}

func (o *ResourceConfig) GetGroupVersionConfigs() map[schema.GroupVersion]bool {
	return o.GroupVersionConfigs
}

// versionAvailable checks if a GroupVersion is available based on its VersionIntroduced and VersionRemoved.
func (o *ResourceConfig) versionAvailable(version schema.GroupVersion) bool {
	if o.GroupVersionRegistry == nil {
		return true
	}
	// compatibilityVersion is not set.
	if o.compatibilityVersion.Major == 0 && o.compatibilityVersion.Minor == 0 {
		return true
	}
	// GroupVersion is introduced after the compatibilityVersion.
	if o.compatibilityVersion.LT(o.GroupVersionLifecycle(version).VersionIntroduced) {
		return false
	}
	// GroupVersion is removed before the compatibilityVersion.
	if o.compatibilityVersion.GT(o.GroupVersionLifecycle(version).VersionRemoved) {
		return false
	}
	// TODO: handle remove when version equal like ShouldServeForVersion.
	return true
}

func (o *ResourceConfig) resourceAvailable(resource schema.GroupVersionResource) bool {
	if o.GroupVersionRegistry == nil {
		return true
	}
	// compatibilityVersion is not set.
	if o.compatibilityVersion.Major == 0 && o.compatibilityVersion.Minor == 0 {
		return true
	}
	// resource is introduced after the compatibilityVersion.
	if o.compatibilityVersion.LT(o.ResourceLifecycle(resource).VersionIntroduced) {
		return false
	}
	// resource is removed before the compatibilityVersion.
	if o.compatibilityVersion.GT(o.ResourceLifecycle(resource).VersionRemoved) {
		return false
	}
	// TODO: handle remove when version equal like ShouldServeForVersion.
	return true
}

func (o *ResourceConfig) DisableResources(resources ...schema.GroupVersionResource) {
	for _, resource := range resources {
		o.ResourceConfigs[resource] = false
	}
}

func (o *ResourceConfig) EnableResources(resources ...schema.GroupVersionResource) {
	for _, resource := range resources {
		if o.resourceAvailable(resource) {
			o.ResourceConfigs[resource] = true
		} else {
			o.ResourceConfigs[resource] = false
			klog.V(1).Infof("resource %s cannot be enabled due to its api lifecyle.", resource.String())
		}
	}
}

func (o *ResourceConfig) ResourceEnabled(resource schema.GroupVersionResource) bool {
	// if a resource is explicitly set, that takes priority over the preference of the version.
	resourceEnabled, explicitlySet := o.ResourceConfigs[resource]
	if explicitlySet {
		return resourceEnabled
	}
	if !o.resourceAvailable(resource) {
		return false
	}
	if !o.versionEnabled(resource.GroupVersion()) {
		return false
	}
	// they are enabled by default.
	return true
}

func (o *ResourceConfig) AnyResourceForGroupEnabled(group string) bool {
	for version := range o.GroupVersionConfigs {
		if version.Group == group {
			if o.versionEnabled(version) {
				return true
			}
		}
	}
	for resource := range o.ResourceConfigs {
		if resource.Group == group && o.ResourceEnabled(resource) {
			return true
		}
	}

	return false
}
