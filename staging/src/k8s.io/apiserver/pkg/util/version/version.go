/*
Copyright 2024 The Kubernetes Authors.

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

package version

import (
	"fmt"
	"sync/atomic"

	"k8s.io/apimachinery/pkg/util/version"
	baseversion "k8s.io/component-base/version"
)

type EffectiveVersion interface {
	BinaryVersion() *version.Version
	EmulationVersion() *version.Version
	MinCompatibilityVersion() *version.Version
	EqualTo(other EffectiveVersion) bool
	String() string
	Validate() []error
}

type MutableEffectiveVersion interface {
	EffectiveVersion
	Set(binaryVersion, emulationVersion, minCompatibilityVersion *version.Version)
	SetEmulationVersion(emulationVersion *version.Version)
	SetMinCompatibilityVersion(minCompatibilityVersion *version.Version)
}

type effectiveVersion struct {
	binaryVersion atomic.Pointer[version.Version]
	// If the emulationVersion is set by the users, it could only contain major and minor versions.
	// In tests, emulationVersion could be the same as the binary version, or set directly,
	// which can have "alpha" as pre-release to continue serving expired apis while we clean up the test.
	emulationVersion atomic.Pointer[version.Version]
	// minCompatibilityVersion could only contain major and minor versions.
	minCompatibilityVersion atomic.Pointer[version.Version]
}

func (m *effectiveVersion) BinaryVersion() *version.Version {
	return m.binaryVersion.Load()
}

func (m *effectiveVersion) EmulationVersion() *version.Version {
	// Emulation version can have "alpha" as pre-release to continue serving expired apis while we clean up the test.
	// The pre-release should not be accessible to the users.
	return m.emulationVersion.Load().WithPreRelease(m.BinaryVersion().PreRelease())
}

func (m *effectiveVersion) MinCompatibilityVersion() *version.Version {
	return m.minCompatibilityVersion.Load()
}

func (m *effectiveVersion) EqualTo(other EffectiveVersion) bool {
	return m.BinaryVersion().EqualTo(other.BinaryVersion()) && m.EmulationVersion().EqualTo(other.EmulationVersion()) && m.MinCompatibilityVersion().EqualTo(other.MinCompatibilityVersion())
}

func (m *effectiveVersion) String() string {
	if m == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{BinaryVersion: %s, EmulationVersion: %s, MinCompatibilityVersion: %s}",
		m.BinaryVersion().String(), m.EmulationVersion().String(), m.MinCompatibilityVersion().String())
}

func (m *effectiveVersion) Set(binaryVersion, emulationVersion, minCompatibilityVersion *version.Version) {
	m.binaryVersion.Store(binaryVersion)
	m.emulationVersion.Store(version.MajorMinor(emulationVersion.Major(), emulationVersion.Minor()))
	m.minCompatibilityVersion.Store(version.MajorMinor(minCompatibilityVersion.Major(), minCompatibilityVersion.Minor()))
}

func (m *effectiveVersion) SetEmulationVersion(emulationVersion *version.Version) {
	m.emulationVersion.Store(version.MajorMinor(emulationVersion.Major(), emulationVersion.Minor()))
}

func (m *effectiveVersion) SetMinCompatibilityVersion(minCompatibilityVersion *version.Version) {
	m.minCompatibilityVersion.Store(version.MajorMinor(minCompatibilityVersion.Major(), minCompatibilityVersion.Minor()))
}

func (m *effectiveVersion) Validate() []error {
	var errs []error
	// Validate only checks the major and minor versions.
	binaryVersion := m.binaryVersion.Load().WithPatch(0)
	emulationVersion := m.emulationVersion.Load()
	minCompatibilityVersion := m.minCompatibilityVersion.Load()

	// emulationVersion can only be 1.{binaryMinor-1}...1.{binaryMinor}.
	maxEmuVer := binaryVersion
	minEmuVer := binaryVersion.SubtractMinor(1)
	// TODO: remove in 1.32
	// emulationVersion is introduced in 1.31, so it cannot be lower than that.
	// binaryVersion could be lower than 1.31 in tests. So we are only checking 1.31.
	if binaryVersion.EqualTo(version.MajorMinor(1, 31)) {
		minEmuVer = version.MajorMinor(1, 31)
	}
	if emulationVersion.GreaterThan(maxEmuVer) || emulationVersion.LessThan(minEmuVer) {
		errs = append(errs, fmt.Errorf("emulation version %s is not between [%s, %s]", emulationVersion.String(), minEmuVer.String(), maxEmuVer.String()))
	}
	// minCompatibilityVersion can only be 1.{binaryMinor-1} for alpha.
	maxCompVer := binaryVersion.SubtractMinor(1)
	minCompVer := binaryVersion.SubtractMinor(1)
	if minCompatibilityVersion.GreaterThan(maxCompVer) || minCompatibilityVersion.LessThan(minCompVer) {
		errs = append(errs, fmt.Errorf("minCompatibilityVersion version %s is not between [%s, %s]", minCompatibilityVersion.String(), minCompVer.String(), maxCompVer.String()))
	}
	return errs
}

func NewEffectiveVersion(binaryVer string) MutableEffectiveVersion {
	effective := &effectiveVersion{}
	binaryVersion := version.MustParse(binaryVer)
	compatVersion := binaryVersion.SubtractMinor(1)
	effective.Set(binaryVersion, binaryVersion, compatVersion)
	return effective
}

// DefaultBuildEffectiveVersion returns the MutableEffectiveVersion based on the
// current build information.
func DefaultBuildEffectiveVersion() MutableEffectiveVersion {
	verInfo := baseversion.Get()
	ver := NewEffectiveVersion(verInfo.String())
	if ver.BinaryVersion().Major() == 0 && ver.BinaryVersion().Minor() == 0 {
		ver = DefaultKubeEffectiveVersion()
	}
	return ver
}

// DefaultKubeEffectiveVersion returns the MutableEffectiveVersion based on the
// latest K8s release.
// Should update for each minor release!
func DefaultKubeEffectiveVersion() MutableEffectiveVersion {
	return NewEffectiveVersion("1.31")
}
