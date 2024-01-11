/*
Copyright 2020 The Kubernetes Authors.

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

package server

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/blang/semver/v4"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/component-base/version"
	"k8s.io/klog/v2"
)

// resourceExpirationEvaluator holds info for deciding if a particular rest.Storage needs to excluded from the API
type resourceExpirationEvaluator struct {
	compatibilityVersion semver.Version
	isAlpha              bool
	// This is usually set for testing for which tests need to be removed.  This prevent insta-failing CI.
	// Set KUBE_APISERVER_STRICT_REMOVED_API_HANDLING_IN_ALPHA to see what will be removed when we tag beta
	strictRemovedHandlingInAlpha bool
	// This is usually set by a cluster-admin looking for a short-term escape hatch after something bad happened.
	// This should be made a flag before merge
	// Set KUBE_APISERVER_SERVE_REMOVED_APIS_FOR_ONE_RELEASE to prevent removing APIs for one more release.
	serveRemovedAPIsOneMoreRelease bool
}

// ResourceExpirationEvaluator indicates whether or not a resource should be served.
type ResourceExpirationEvaluator interface {
	// RemoveDeletedKinds inspects the storage map and modifies it in place by removing storage for kinds that have been deleted.
	// versionedResourcesStorageMap mirrors the field on APIGroupInfo, it's a map from version to resource to the storage.
	RemoveDeletedKinds(groupName string, versioner runtime.ObjectVersioner, versionedResourcesStorageMap map[string]map[string]rest.Storage)
	// ShouldServeForVersion returns true if a particular version cut off is after the current version
	ShouldServeForVersion(majorRemoved, minorRemoved int) bool
}

func NewResourceExpirationEvaluator(compatibilityGitVersion string) (ResourceExpirationEvaluator, error) {
	ret := &resourceExpirationEvaluator{
		strictRemovedHandlingInAlpha: false,
	}
	klog.V(1).Infof("NewResourceExpirationEvaluator with compatibilityGitVersion = %s.", compatibilityGitVersion)
	if ver, err := version.ParseVersion(compatibilityGitVersion); err != nil {
		return nil, err
	} else {
		ret.compatibilityVersion = ver
	}
	ret.isAlpha = strings.Contains(compatibilityGitVersion, "alpha")

	if envString, ok := os.LookupEnv("KUBE_APISERVER_STRICT_REMOVED_API_HANDLING_IN_ALPHA"); !ok {
		// do nothing
	} else if envBool, err := strconv.ParseBool(envString); err != nil {
		return nil, err
	} else {
		ret.strictRemovedHandlingInAlpha = envBool
	}

	if envString, ok := os.LookupEnv("KUBE_APISERVER_SERVE_REMOVED_APIS_FOR_ONE_RELEASE"); !ok {
		// do nothing
	} else if envBool, err := strconv.ParseBool(envString); err != nil {
		return nil, err
	} else {
		ret.serveRemovedAPIsOneMoreRelease = envBool
	}

	return ret, nil
}

func (e *resourceExpirationEvaluator) shouldServe(gv schema.GroupVersion, versioner runtime.ObjectVersioner, resourceServingInfo rest.Storage, resourceName string) bool {
	internalPtr := resourceServingInfo.New()

	target := gv
	// honor storage that overrides group version (used for things like scale subresources)
	if versionProvider, ok := resourceServingInfo.(rest.GroupVersionKindProvider); ok {
		target = versionProvider.GroupVersionKind(target).GroupVersion()
	}

	versionedPtr, err := versioner.ConvertToVersion(internalPtr, target)
	if err != nil {
		utilruntime.HandleError(err)
		return false
	}

	if !e.shouldServeForIntroduced(versionedPtr, gv.WithResource(resourceName)) {
		return false
	}

	removed, ok := versionedPtr.(removedInterface)
	if !ok {
		return true
	}
	majorRemoved, minorRemoved := removed.APILifecycleRemoved()
	fmt.Printf("sizhangDebug: resource %s/%s removed at %d.%d\n",
		gv.String(), resourceName, majorRemoved, minorRemoved)
	return e.ShouldServeForVersion(majorRemoved, minorRemoved)
}

// shouldServeForIntroduced evalues if the resource should be served based on APILifecycleIntroduced.
// It could return false if an api is introduced at some binary version while the compatibility version is still old.
func (e *resourceExpirationEvaluator) shouldServeForIntroduced(obj runtime.Object, gvs schema.GroupVersionResource) bool {
	introduced, ok := obj.(introducedInterface)
	if !ok {
		return true
	}
	majorIntroduced, minorIntroduced := introduced.APILifecycleIntroduced()
	fmt.Printf("sizhangDebug: resource %s introduced at %d.%d\n", gvs.String(), majorIntroduced, minorIntroduced)
	introducedVersion := semver.Version{Major: uint64(majorIntroduced), Minor: uint64(minorIntroduced)}
	if isNonDefaultVersion(e.compatibilityVersion) && introducedVersion.GT(e.compatibilityVersion) {
		fmt.Printf("sizhangDebug: resource %s introduced at %d.%d, later than compatibilityVersion %v\n",
			gvs.String(), majorIntroduced, minorIntroduced, e.compatibilityVersion)
		return false
	}
	return true
}

// ShouldServeForVersion evalues if the resource should be served based on the version it is removed.
// It could return false if compatibility version is newer than the version the api is removed.
func (e *resourceExpirationEvaluator) ShouldServeForVersion(majorRemoved, minorRemoved int) bool {
	removedVersion := semver.Version{Major: uint64(majorRemoved), Minor: uint64(minorRemoved)}
	if e.compatibilityVersion.LT(removedVersion) {
		return true
	}
	if e.compatibilityVersion.GT(removedVersion) {
		return false
	}
	// at this point major and minor are equal, so this API should be removed when the current release GAs.
	// If this is an alpha tag, don't remove by default, but allow the option.
	// If the cluster-admin has requested serving one more release, allow it.
	if e.isAlpha && e.strictRemovedHandlingInAlpha { // don't serve in alpha if we want strict handling
		return false
	}
	if e.isAlpha { // alphas are allowed to continue serving expired betas while we clean up the test
		return true
	}
	if e.serveRemovedAPIsOneMoreRelease { // cluster-admins are allowed to kick the can one release down the road
		return true
	}
	return false
}

type removedInterface interface {
	APILifecycleRemoved() (major, minor int)
}

type introducedInterface interface {
	APILifecycleIntroduced() (major, minor int)
}

// removeDeletedKinds inspects the storage map and modifies it in place by removing storage for kinds that have been deleted.
// versionedResourcesStorageMap mirrors the field on APIGroupInfo, it's a map from version to resource to the storage.
func (e *resourceExpirationEvaluator) RemoveDeletedKinds(groupName string, versioner runtime.ObjectVersioner, versionedResourcesStorageMap map[string]map[string]rest.Storage) {
	versionsToRemove := sets.NewString()
	for apiVersion := range sets.StringKeySet(versionedResourcesStorageMap) {
		versionToResource := versionedResourcesStorageMap[apiVersion]
		resourcesToRemove := sets.NewString()
		for resourceName, resourceServingInfo := range versionToResource {
			if !e.shouldServe(schema.GroupVersion{Group: groupName, Version: apiVersion}, versioner, resourceServingInfo, resourceName) {
				fmt.Printf("sizhangDebug: should not serve %s/%s/%s\n", groupName, apiVersion, resourceName)
				resourcesToRemove.Insert(resourceName)
			} else {
				fmt.Printf("sizhangDebug: should serve %s/%s/%s\n", groupName, apiVersion, resourceName)
			}
		}

		for resourceName := range versionedResourcesStorageMap[apiVersion] {
			if !shouldRemoveResourceAndSubresources(resourcesToRemove, resourceName) {
				continue
			}

			klog.V(1).Infof("Removing resource %v.%v.%v because it is time to stop serving it per APILifecycle.", resourceName, apiVersion, groupName)
			delete(versionToResource, resourceName)
		}
		versionedResourcesStorageMap[apiVersion] = versionToResource

		if len(versionedResourcesStorageMap[apiVersion]) == 0 {
			versionsToRemove.Insert(apiVersion)
		}
	}

	for _, apiVersion := range versionsToRemove.List() {
		klog.V(1).Infof("Removing version %v.%v because it is time to stop serving it because it has no resources per APILifecycle.", apiVersion, groupName)
		delete(versionedResourcesStorageMap, apiVersion)
	}
}

func shouldRemoveResourceAndSubresources(resourcesToRemove sets.String, resourceName string) bool {
	for _, resourceToRemove := range resourcesToRemove.List() {
		if resourceName == resourceToRemove {
			return true
		}
		// our API works on nesting, so you can have deployments, deployments/status, and deployments/scale.  Not all subresources
		// serve the parent type, but if the parent type (deployments in this case), has been removed, it's subresources should be removed too.
		if strings.HasPrefix(resourceName, resourceToRemove+"/") {
			return true
		}
	}
	return false
}

func isNonDefaultVersion(ver semver.Version) bool {
	return ver.Major > 0 || ver.Minor > 0
}
