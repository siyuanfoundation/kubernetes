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

package featuregate

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/blang/semver/v4"
	"github.com/spf13/pflag"

	"k8s.io/apimachinery/pkg/util/naming"
	featuremetrics "k8s.io/component-base/metrics/prometheus/feature"
	"k8s.io/component-base/version"
	"k8s.io/klog/v2"
)

type Feature string

const (
	flagName = "feature-gates"

	// allAlphaGate is a global toggle for alpha features. Per-feature key
	// values override the default set by allAlphaGate. Examples:
	//   AllAlpha=false,NewFeature=true  will result in newFeature=true
	//   AllAlpha=true,NewFeature=false  will result in newFeature=false
	allAlphaGate Feature = "AllAlpha"

	// allBetaGate is a global toggle for beta features. Per-feature key
	// values override the default set by allBetaGate. Examples:
	//   AllBeta=false,NewFeature=true  will result in NewFeature=true
	//   AllBeta=true,NewFeature=false  will result in NewFeature=false
	allBetaGate Feature = "AllBeta"
)

var (
	// The generic features.
	defaultFeatures = map[Feature]VersionedSpecs{
		allAlphaGate: {{Default: false, PreRelease: Alpha}},
		allBetaGate:  {{Default: false, PreRelease: Beta}},
	}

	// Special handling for a few gates.
	specialFeatures = map[Feature]func(known map[Feature]VersionedSpecs, enabled map[Feature]bool, val bool, cVer semver.Version){
		allAlphaGate: setUnsetAlphaGates,
		allBetaGate:  setUnsetBetaGates,
	}

	ErrMajorAndMinorOnly = errors.New("version string must only contain major and minor")
)

type FeatureSpec struct {
	// Default is the default enablement state for the feature
	Default bool
	// LockToDefault indicates that the feature is locked to its default and cannot be changed
	LockToDefault bool
	// PreRelease indicates the current maturity level of the feature
	PreRelease prerelease
	// Version indicates the version from which this configuration is valid.
	Version semver.Version
}

type VersionedSpecs []FeatureSpec

func (g VersionedSpecs) Len() int           { return len(g) }
func (g VersionedSpecs) Less(i, j int) bool { return g[i].Version.LT(g[j].Version) }
func (g VersionedSpecs) Swap(i, j int)      { g[i], g[j] = g[j], g[i] }

type PromotionVersionMapping map[prerelease]string

type prerelease string

const (
	PreAlpha = prerelease("PRE-ALPHA")
	// Values for PreRelease.
	Alpha = prerelease("ALPHA")
	Beta  = prerelease("BETA")
	GA    = prerelease("")

	// Deprecated
	Deprecated = prerelease("DEPRECATED")
)

// FeatureGate indicates whether a given feature is enabled or not
type FeatureGate interface {
	// Enabled returns true if the key is enabled.
	Enabled(key Feature) bool
	// StabilityLevel returns the stability level (prerelease+versionString) of the key exists as seen by the CompatibilityVersion, an error otherwise
	StabilityLevel(key Feature) (prerelease, string, error)
	// KnownFeatures returns a slice of strings describing the FeatureGate's known features.
	KnownFeatures() []string
	// DeepCopy returns a deep copy of the FeatureGate object, such that gates can be
	// set on the copy without mutating the original. This is useful for validating
	// config against potential feature gate changes before committing those changes.
	DeepCopy() MutableFeatureGate
	// SetCompatibilityVersion changes compatibilityVersion from default binary version to the specified version.
	SetCompatibilityVersion(v string)
	GetCompatibilityVersion() semver.Version
}

// MutableFeatureGate parses and stores flag gates for known features from
// a string like feature1=true,feature2=false,...
type MutableFeatureGate interface {
	FeatureGate

	// AddFlag adds a flag for setting global feature gates to the specified FlagSet.
	AddFlag(fs *pflag.FlagSet)
	// Set parses and stores flag gates for known features
	// from a string like feature1=true,feature2=false,...
	Set(value string) error
	// SetFromMap stores flag gates for known features from a map[string]bool or returns an error
	SetFromMap(m map[string]bool) error
	// Add adds features to the featureGate.
	Add(features map[Feature]FeatureSpec) error
	// AddVersioned adds versioned feature specs to the featureGate.
	AddVersioned(features map[Feature]VersionedSpecs) error
	// GetAll returns a copy of the map of known feature names to feature specs for the current compatibilityVersion.
	GetAll() map[Feature]FeatureSpec
	// GetAll returns a copy of the map of known feature names to versioned feature specs.
	GetAllVersioned() map[Feature]VersionedSpecs
	// AddMetrics adds feature enablement metrics
	AddMetrics()
}

// featureGate implements FeatureGate as well as pflag.Value for flag parsing.
type featureGate struct {
	featureGateName string

	special map[Feature]func(map[Feature]VersionedSpecs, map[Feature]bool, bool, semver.Version)

	// lock guards writes to known, enabled, and reads/writes of closed
	lock sync.Mutex
	// known holds a map[Feature]FeatureSpec
	known *atomic.Value
	// enabled holds a map[Feature]bool
	enabled *atomic.Value
	// closed is set to true when AddFlag is called, and prevents subsequent calls to Add
	closed bool

	compatibilityVersion semver.Version

	binaryVersion semver.Version
}

func setUnsetAlphaGates(known map[Feature]VersionedSpecs, enabled map[Feature]bool, val bool, cVer semver.Version) {
	for k, v := range known {
		if k == "AllAlpha" || k == "AllBeta" {
			continue
		}
		currentVersion := getCurrentVersion(v, cVer)
		if currentVersion.PreRelease == Alpha {
			if _, found := enabled[k]; !found {
				enabled[k] = val
			}
		}
	}
}

func setUnsetBetaGates(known map[Feature]VersionedSpecs, enabled map[Feature]bool, val bool, cVer semver.Version) {
	for k, v := range known {
		if k == "AllAlpha" || k == "AllBeta" {
			continue
		}
		currentVersion := getCurrentVersion(v, cVer)
		if currentVersion.PreRelease == Beta {
			if _, found := enabled[k]; !found {
				enabled[k] = val
			}
		}
	}
}

// Set, String, and Type implement pflag.Value
var _ pflag.Value = &featureGate{}

// internalPackages are packages that ignored when creating a name for featureGates. These packages are in the common
// call chains, so they'd be unhelpful as names.
var internalPackages = []string{"k8s.io/component-base/featuregate/feature_gate.go"}

func newFeatureGate(binaryVersion, compatibilityVersion string) *featureGate {
	known := map[Feature]VersionedSpecs{}
	for k, v := range defaultFeatures {
		known[k] = v
	}

	knownValue := &atomic.Value{}
	knownValue.Store(known)

	enabled := map[Feature]bool{}
	enabledValue := &atomic.Value{}
	enabledValue.Store(enabled)

	f := &featureGate{
		featureGateName: naming.GetNameFromCallsite(internalPackages...),
		known:           knownValue,
		special:         specialFeatures,
		enabled:         enabledValue,
	}
	f.binaryVersion = version.MustParseVersion(binaryVersion)
	f.compatibilityVersion = version.MustParseVersion(compatibilityVersion)
	return f
}

func NewFeatureGate() *featureGate {
	binaryVersison := getBinaryVersion()
	// set compatibilityVersion to be equal to binaryVersion initially.
	// can be changed with SetCompatibilityVersion later.
	return newFeatureGate(binaryVersison, binaryVersison)
}

func NewFeatureGateForTest(binaryVersion string) *featureGate {
	return newFeatureGate(binaryVersion, binaryVersion)
}

// Set parses a string of the form "key1=value1,key2=value2,..." into a
// map[string]bool of known keys or returns an error.
func (f *featureGate) Set(value string) error {
	m := make(map[string]bool)
	for _, s := range strings.Split(value, ",") {
		if len(s) == 0 {
			continue
		}
		arr := strings.SplitN(s, "=", 2)
		k := strings.TrimSpace(arr[0])
		if len(arr) != 2 {
			return fmt.Errorf("missing bool value for %s", k)
		}
		v := strings.TrimSpace(arr[1])
		boolValue, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid value of %s=%s, err: %v", k, v, err)
		}
		m[k] = boolValue
	}
	return f.SetFromMap(m)
}

// SetFromMap stores flag gates for known features from a map[string]bool or returns an error
func (f *featureGate) SetFromMap(m map[string]bool) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	// Copy existing state
	known := map[Feature]VersionedSpecs{}
	for k, v := range f.known.Load().(map[Feature]VersionedSpecs) {
		sort.Sort(v)
		known[k] = v
	}
	enabled := map[Feature]bool{}
	for k, v := range f.enabled.Load().(map[Feature]bool) {
		enabled[k] = v
	}

	for k, v := range m {
		key := Feature(k)
		versionedSpecs, ok := known[key]
		if !ok {
			return fmt.Errorf("unrecognized feature gate: %s", k)
		}
		currentVersion := f.getCurrentVersion(versionedSpecs)
		if currentVersion.LockToDefault && currentVersion.Default != v {
			return fmt.Errorf("cannot set feature gate %v to %v, feature is locked to %v", k, v, currentVersion.Default)
		}
		enabled[key] = v
		// Handle "special" features like "all alpha gates"
		if fn, found := f.special[key]; found {
			fn(known, enabled, v, f.compatibilityVersion)
		}

		if currentVersion.PreRelease == Deprecated {
			klog.Warningf("Setting deprecated feature gate %s=%t. It will be removed in a future release.", k, v)
		} else if currentVersion.PreRelease == GA {
			klog.Warningf("Setting GA feature gate %s=%t. It will be removed in a future release.", k, v)
		}
	}

	// Persist changes
	f.known.Store(known)
	f.enabled.Store(enabled)

	klog.V(1).Infof("feature gates: %v", f.enabled)
	return nil
}

// String returns a string containing all enabled feature gates, formatted as "key1=value1,key2=value2,...".
func (f *featureGate) String() string {
	pairs := []string{}
	for k, v := range f.enabled.Load().(map[Feature]bool) {
		pairs = append(pairs, fmt.Sprintf("%s=%t", k, v))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (f *featureGate) Type() string {
	return "mapStringBool"
}

// Add adds features to the featureGate.
func (f *featureGate) Add(features map[Feature]FeatureSpec) error {
	vs := map[Feature]VersionedSpecs{}
	for name, spec := range features {
		// if no version is provided for the FeatureSpec, it is defaulted to the binary version.
		spec.Version = f.binaryVersion
		vs[name] = VersionedSpecs{spec}
	}
	return f.AddVersioned(vs)
}

// AddVersioned adds versioned feature specs to the featureGate.
func (f *featureGate) AddVersioned(features map[Feature]VersionedSpecs) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.closed {
		return fmt.Errorf("cannot add a feature gate after adding it to the flag set")
	}

	// Copy existing state
	known := map[Feature]VersionedSpecs{}
	for k, v := range f.known.Load().(map[Feature]VersionedSpecs) {
		known[k] = v
	}

	for name, specs := range features {
		sort.Sort(specs)
		if existingSpec, found := known[name]; found {
			sort.Sort(existingSpec)
			if reflect.DeepEqual(existingSpec, specs) {
				continue
			}
			return fmt.Errorf("feature gate %q with different spec already exists: %v", name, existingSpec)
		}
		known[name] = specs
	}

	// Persist updated state
	f.known.Store(known)

	return nil
}

// GetAll returns a copy of the map of known feature names to feature specs for the current compatibilityVersion.
func (f *featureGate) GetAll() map[Feature]FeatureSpec {
	retval := map[Feature]FeatureSpec{}
	for k, v := range f.GetAllVersioned() {
		retval[k] = f.getCurrentVersion(v)
	}
	return retval
}

// GetAllVersioned returns a copy of the map of known feature names to versioned feature specs.
func (f *featureGate) GetAllVersioned() map[Feature]VersionedSpecs {
	retval := map[Feature]VersionedSpecs{}
	for k, v := range f.known.Load().(map[Feature]VersionedSpecs) {
		retval[k] = v
	}
	return retval
}

// SetCompatibilityVersion changes compatibilityVersion from default binary version to the specified version.
func (f *featureGate) SetCompatibilityVersion(v string) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.compatibilityVersion = version.MustParseVersion(v)
}

func (f *featureGate) GetCompatibilityVersion() semver.Version {
	return f.compatibilityVersion
}

func (f *featureGate) StabilityLevel(key Feature) (prerelease, string, error) {
	if v, ok := f.known.Load().(map[Feature]VersionedSpecs)[key]; ok {
		currentVersion := f.getCurrentVersion(v)
		return currentVersion.PreRelease, fmt.Sprintf("%d.%d", currentVersion.Version.Major, currentVersion.Version.Minor), nil
	}
	return PreAlpha, "0.0", fmt.Errorf("feature %q is not registered in FeatureGate %q", key, f.featureGateName)
}

// Enabled returns true if the key is enabled.  If the key is not known, this call will panic.
func (f *featureGate) Enabled(key Feature) bool {
	// fallback to default behavior, since we don't have compatibility version set
	if v, ok := f.enabled.Load().(map[Feature]bool)[key]; ok {
		return v
	}
	if v, ok := f.known.Load().(map[Feature]VersionedSpecs)[key]; ok {
		return f.getCurrentVersion(v).Default
	}

	panic(fmt.Errorf("feature %q is not registered in FeatureGate %q", key, f.featureGateName))
}

func (f *featureGate) getCurrentVersion(v VersionedSpecs) FeatureSpec {
	return getCurrentVersion(v, f.compatibilityVersion)
}

func getCurrentVersion(v VersionedSpecs, compatibilityVersion semver.Version) FeatureSpec {
	i := len(v) - 1
	for ; i >= 0; i-- {
		if v[i].Version.GT(compatibilityVersion) {
			continue
		}
		return v[i]
	}
	return FeatureSpec{
		Default:    false,
		PreRelease: PreAlpha,
	}
}

func getBinaryVersion() string {
	gitVersion := version.MustParseVersion(version.Get().GitVersion)
	return fmt.Sprintf("%d.%d", gitVersion.Major, gitVersion.Minor)
}

// AddFlag adds a flag for setting global feature gates to the specified FlagSet.
func (f *featureGate) AddFlag(fs *pflag.FlagSet) {
	f.lock.Lock()
	// TODO(mtaufen): Shouldn't we just close it on the first Set/SetFromMap instead?
	// Not all components expose a feature gates flag using this AddFlag method, and
	// in the future, all components will completely stop exposing a feature gates flag,
	// in favor of componentconfig.
	f.closed = true
	f.lock.Unlock()

	known := f.KnownFeatures()
	fs.Var(f, flagName, ""+
		"A set of key=value pairs that describe feature gates for alpha/experimental features. "+
		"Options are:\n"+strings.Join(known, "\n"))
}

func (f *featureGate) AddMetrics() {
	for feature, featureSpec := range f.GetAll() {
		featuremetrics.RecordFeatureInfo(context.Background(), string(feature), string(featureSpec.PreRelease), f.Enabled(feature))
	}
}

// KnownFeatures returns a slice of strings describing the FeatureGate's known features.
// preAlpha, Deprecated and GA features are hidden from the list.
func (f *featureGate) KnownFeatures() []string {
	var known []string
	for k, v := range f.known.Load().(map[Feature]VersionedSpecs) {
		if k == "AllAlpha" || k == "AllBeta" {
			known = append(known, fmt.Sprintf("%s=true|false (%s - default=%t)", k, v[0].PreRelease, v[0].Default))
			continue
		}
		currentV := f.getCurrentVersion(v)
		if currentV.PreRelease == GA || currentV.PreRelease == Deprecated || currentV.PreRelease == PreAlpha {
			continue
		}
		known = append(known, fmt.Sprintf("%s=true|false (%s - default=%t)", k, currentV.PreRelease, currentV.Default))
	}
	sort.Strings(known)
	return known
}

// DeepCopy returns a deep copy of the FeatureGate object, such that gates can be
// set on the copy without mutating the original. This is useful for validating
// config against potential feature gate changes before committing those changes.
func (f *featureGate) DeepCopy() MutableFeatureGate {
	// Copy existing state.
	known := map[Feature]VersionedSpecs{}
	for k, v := range f.known.Load().(map[Feature]VersionedSpecs) {
		known[k] = v
	}
	enabled := map[Feature]bool{}
	for k, v := range f.enabled.Load().(map[Feature]bool) {
		enabled[k] = v
	}

	// Store copied state in new atomics.
	knownValue := &atomic.Value{}
	knownValue.Store(known)
	enabledValue := &atomic.Value{}
	enabledValue.Store(enabled)

	// Construct a new featureGate around the copied state.
	// Note that specialFeatures is treated as immutable by convention,
	// and we maintain the value of f.closed across the copy.
	return &featureGate{
		special:              specialFeatures,
		known:                knownValue,
		enabled:              enabledValue,
		closed:               f.closed,
		binaryVersion:        f.binaryVersion,
		compatibilityVersion: f.compatibilityVersion,
	}
}
