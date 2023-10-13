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
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"

	"k8s.io/component-base/metrics/legacyregistry"
	featuremetrics "k8s.io/component-base/metrics/prometheus/feature"
	"k8s.io/component-base/metrics/testutil"
)

func TestFeatureGateFlag(t *testing.T) {
	// gates for testing
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"
	const testDeprecatedGate Feature = "TestDeprecated"

	tests := []struct {
		arg        string
		expect     map[Feature]bool
		parseError string
	}{
		{
			arg: "",
			expect: map[Feature]bool{
				allAlphaGate:       false,
				allBetaGate:        false,
				testAlphaGate:      false,
				testBetaGate:       false,
				testDeprecatedGate: false,
			},
		},
		{
			arg: "TestDeprecated=true",
			expect: map[Feature]bool{
				allAlphaGate:       false,
				allBetaGate:        false,
				testAlphaGate:      false,
				testBetaGate:       false,
				testDeprecatedGate: true,
			},
		},
		{
			arg: "fooBarBaz=true",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
			parseError: "unrecognized feature gate: fooBarBaz",
		},
		{
			arg: "AllAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			arg: "AllAlpha=true",
			expect: map[Feature]bool{
				allAlphaGate:  true,
				allBetaGate:   false,
				testAlphaGate: true,
				testBetaGate:  false,
			},
		},
		{
			arg: "AllAlpha=banana",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
			parseError: "invalid value of AllAlpha",
		},
		{
			arg: "AllAlpha=false,TestAlpha=true",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: true,
				testBetaGate:  false,
			},
		},
		{
			arg: "TestAlpha=true,AllAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: true,
				testBetaGate:  false,
			},
		},
		{
			arg: "AllAlpha=true,TestAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:  true,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			arg: "TestAlpha=false,AllAlpha=true",
			expect: map[Feature]bool{
				allAlphaGate:  true,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			arg: "TestBeta=true,AllAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  true,
			},
		},

		{
			arg: "AllBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			arg: "AllBeta=true",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   true,
				testAlphaGate: false,
				testBetaGate:  true,
			},
		},
		{
			arg: "AllBeta=banana",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  false,
			},
			parseError: "invalid value of AllBeta",
		},
		{
			arg: "AllBeta=false,TestBeta=true",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  true,
			},
		},
		{
			arg: "TestBeta=true,AllBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: false,
				testBetaGate:  true,
			},
		},
		{
			arg: "AllBeta=true,TestBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   true,
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			arg: "TestBeta=false,AllBeta=true",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   true,
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			arg: "TestAlpha=true,AllBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:  false,
				allBetaGate:   false,
				testAlphaGate: true,
				testBetaGate:  false,
			},
		},
	}
	for i, test := range tests {
		t.Run(test.arg, func(t *testing.T) {
			fs := pflag.NewFlagSet("testfeaturegateflag", pflag.ContinueOnError)
			f := NewFeatureGateForTest("1.29")
			f.Add(map[Feature]FeatureSpec{
				testAlphaGate:      {Default: false, PreRelease: Alpha},
				testBetaGate:       {Default: false, PreRelease: Beta},
				testDeprecatedGate: {Default: false, PreRelease: Deprecated},
			})
			f.AddFlag(fs)

			err := fs.Parse([]string{fmt.Sprintf("--%s=%s", flagName, test.arg)})
			if test.parseError != "" {
				if !strings.Contains(err.Error(), test.parseError) {
					t.Errorf("%d: Parse() Expected %v, Got %v", i, test.parseError, err)
				}
			} else if err != nil {
				t.Errorf("%d: Parse() Expected nil, Got %v", i, err)
			}
			for k, v := range test.expect {
				if actual := f.enabled.Load().(map[Feature]bool)[k]; actual != v {
					t.Errorf("%d: expected %s=%v, Got %v", i, k, v, actual)
				}
			}
		})
	}
}

func TestFeatureGateOverride(t *testing.T) {
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"

	// Don't parse the flag, assert defaults are used.
	var f *featureGate = NewFeatureGateForTest("1.29")
	f.Add(map[Feature]FeatureSpec{
		testAlphaGate: {Default: false, PreRelease: Alpha},
		testBetaGate:  {Default: false, PreRelease: Beta},
	})

	f.Set("TestAlpha=true,TestBeta=true")
	if f.Enabled(testAlphaGate) != true {
		t.Errorf("Expected true")
	}
	if f.Enabled(testBetaGate) != true {
		t.Errorf("Expected true")
	}

	f.Set("TestAlpha=false")
	if f.Enabled(testAlphaGate) != false {
		t.Errorf("Expected false")
	}
	if f.Enabled(testBetaGate) != true {
		t.Errorf("Expected true")
	}
}

func TestFeatureGateFlagDefaults(t *testing.T) {
	// gates for testing
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"

	// Don't parse the flag, assert defaults are used.
	var f *featureGate = NewFeatureGateForTest("1.29")
	f.Add(map[Feature]FeatureSpec{
		testAlphaGate: {Default: false, PreRelease: Alpha},
		testBetaGate:  {Default: true, PreRelease: Beta},
	})

	if f.Enabled(testAlphaGate) != false {
		t.Errorf("Expected false")
	}
	if f.Enabled(testBetaGate) != true {
		t.Errorf("Expected true")
	}
}

func TestFeatureGateKnownFeatures(t *testing.T) {
	// gates for testing
	const (
		testAlphaGate      Feature = "TestAlpha"
		testBetaGate       Feature = "TestBeta"
		testGAGate         Feature = "TestGA"
		testDeprecatedGate Feature = "TestDeprecated"
	)

	// Don't parse the flag, assert defaults are used.
	var f *featureGate = NewFeatureGateForTest("1.29")
	f.Add(map[Feature]FeatureSpec{
		testAlphaGate:      {Default: false, PreRelease: Alpha},
		testBetaGate:       {Default: true, PreRelease: Beta},
		testGAGate:         {Default: true, PreRelease: GA},
		testDeprecatedGate: {Default: false, PreRelease: Deprecated},
	})

	known := strings.Join(f.KnownFeatures(), " ")

	assert.Contains(t, known, testAlphaGate)
	assert.Contains(t, known, testBetaGate)
	assert.NotContains(t, known, testGAGate)
	assert.NotContains(t, known, testDeprecatedGate)
}

func TestFeatureGateSetFromMap(t *testing.T) {
	// gates for testing
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"
	const testLockedTrueGate Feature = "TestLockedTrue"
	const testLockedFalseGate Feature = "TestLockedFalse"

	tests := []struct {
		name        string
		setmap      map[string]bool
		expect      map[Feature]bool
		setmapError string
	}{
		{
			name: "set TestAlpha and TestBeta true",
			setmap: map[string]bool{
				"TestAlpha": true,
				"TestBeta":  true,
			},
			expect: map[Feature]bool{
				testAlphaGate: true,
				testBetaGate:  true,
			},
		},
		{
			name: "set TestBeta true",
			setmap: map[string]bool{
				"TestBeta": true,
			},
			expect: map[Feature]bool{
				testAlphaGate: false,
				testBetaGate:  true,
			},
		},
		{
			name: "set TestAlpha false",
			setmap: map[string]bool{
				"TestAlpha": false,
			},
			expect: map[Feature]bool{
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			name: "set TestInvaild true",
			setmap: map[string]bool{
				"TestInvaild": true,
			},
			expect: map[Feature]bool{
				testAlphaGate: false,
				testBetaGate:  false,
			},
			setmapError: "unrecognized feature gate:",
		},
		{
			name: "set locked gates",
			setmap: map[string]bool{
				"TestLockedTrue":  true,
				"TestLockedFalse": false,
			},
			expect: map[Feature]bool{
				testAlphaGate: false,
				testBetaGate:  false,
			},
		},
		{
			name: "set locked gates",
			setmap: map[string]bool{
				"TestLockedTrue": false,
			},
			expect: map[Feature]bool{
				testAlphaGate: false,
				testBetaGate:  false,
			},
			setmapError: "cannot set feature gate TestLockedTrue to false, feature is locked to true",
		},
		{
			name: "set locked gates",
			setmap: map[string]bool{
				"TestLockedFalse": true,
			},
			expect: map[Feature]bool{
				testAlphaGate: false,
				testBetaGate:  false,
			},
			setmapError: "cannot set feature gate TestLockedFalse to true, feature is locked to false",
		},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("SetFromMap %s", test.name), func(t *testing.T) {
			f := NewFeatureGateForTest("1.29")
			f.Add(map[Feature]FeatureSpec{
				testAlphaGate:       {Default: false, PreRelease: Alpha},
				testBetaGate:        {Default: false, PreRelease: Beta},
				testLockedTrueGate:  {Default: true, PreRelease: GA, LockToDefault: true},
				testLockedFalseGate: {Default: false, PreRelease: GA, LockToDefault: true},
			})
			err := f.SetFromMap(test.setmap)
			if test.setmapError != "" {
				if err == nil {
					t.Errorf("expected error, got none")
				} else if !strings.Contains(err.Error(), test.setmapError) {
					t.Errorf("%d: SetFromMap(%#v) Expected err:%v, Got err:%v", i, test.setmap, test.setmapError, err)
				}
			} else if err != nil {
				t.Errorf("%d: SetFromMap(%#v) Expected success, Got err:%v", i, test.setmap, err)
			}
			for k, v := range test.expect {
				if actual := f.Enabled(k); actual != v {
					t.Errorf("%d: SetFromMap(%#v) Expected %s=%v, Got %s=%v", i, test.setmap, k, v, k, actual)
				}
			}
		})
	}
}

func TestFeatureGateMetrics(t *testing.T) {
	// gates for testing
	featuremetrics.ResetFeatureInfoMetric()
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"
	const testAlphaEnabled Feature = "TestAlphaEnabled"
	const testBetaDisabled Feature = "TestBetaDisabled"
	testedMetrics := []string{"kubernetes_feature_enabled"}
	expectedOutput := `
		# HELP kubernetes_feature_enabled [BETA] This metric records the data about the stage and enablement of a k8s feature.
        # TYPE kubernetes_feature_enabled gauge
        kubernetes_feature_enabled{name="TestAlpha",stage="ALPHA"} 0
        kubernetes_feature_enabled{name="TestBeta",stage="BETA"} 1
		kubernetes_feature_enabled{name="TestAlphaEnabled",stage="ALPHA"} 1
        kubernetes_feature_enabled{name="AllAlpha",stage="ALPHA"} 0
        kubernetes_feature_enabled{name="AllBeta",stage="BETA"} 0
		kubernetes_feature_enabled{name="TestBetaDisabled",stage="ALPHA"} 0
`

	f := NewFeatureGateForTest("1.29")
	fMap := map[Feature]FeatureSpec{
		testAlphaGate:    {Default: false, PreRelease: Alpha},
		testAlphaEnabled: {Default: false, PreRelease: Alpha},
		testBetaGate:     {Default: true, PreRelease: Beta},
		testBetaDisabled: {Default: true, PreRelease: Alpha},
	}
	f.Add(fMap)
	f.SetFromMap(map[string]bool{"TestAlphaEnabled": true, "TestBetaDisabled": false})
	f.AddMetrics()
	if err := testutil.GatherAndCompare(legacyregistry.DefaultGatherer, strings.NewReader(expectedOutput), testedMetrics...); err != nil {
		t.Fatal(err)
	}
}

func TestFeatureGateString(t *testing.T) {
	// gates for testing
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"
	const testGAGate Feature = "TestGA"

	featuremap := map[Feature]FeatureSpec{
		testGAGate:    {Default: true, PreRelease: GA},
		testAlphaGate: {Default: false, PreRelease: Alpha},
		testBetaGate:  {Default: true, PreRelease: Beta},
	}

	tests := []struct {
		setmap map[string]bool
		expect string
	}{
		{
			setmap: map[string]bool{
				"TestAlpha": false,
			},
			expect: "TestAlpha=false",
		},
		{
			setmap: map[string]bool{
				"TestAlpha": false,
				"TestBeta":  true,
			},
			expect: "TestAlpha=false,TestBeta=true",
		},
		{
			setmap: map[string]bool{
				"TestGA":    true,
				"TestAlpha": false,
				"TestBeta":  true,
			},
			expect: "TestAlpha=false,TestBeta=true,TestGA=true",
		},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("SetFromMap %s", test.expect), func(t *testing.T) {
			f := NewFeatureGateForTest("1.29")
			f.Add(featuremap)
			f.SetFromMap(test.setmap)
			result := f.String()
			if result != test.expect {
				t.Errorf("%d: SetFromMap(%#v) Expected %s, Got %s", i, test.setmap, test.expect, result)
			}
		})
	}
}

func TestVersionedFeatureGateFlag(t *testing.T) {
	// gates for testing
	const testGAGate Feature = "TestGA"
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"
	const testAlphaGateNoVersion Feature = "TestAlphaNoVersion"
	const testBetaGateNoVersion Feature = "TestBetaNoVersion"

	tests := []struct {
		arg        string
		expect     map[Feature]bool
		parseError string
	}{
		{
			arg: "",
			expect: map[Feature]bool{
				testGAGate:             false,
				allAlphaGate:           false,
				allBetaGate:            false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "fooBarBaz=true",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
			parseError: "unrecognized feature gate: fooBarBaz",
		},
		{
			arg: "AllAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "AllAlpha=true",
			expect: map[Feature]bool{
				allAlphaGate:           true,
				allBetaGate:            false,
				testAlphaGate:          false,
				testGAGate:             false,
				testBetaGate:           true,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "AllAlpha=banana",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
			parseError: "invalid value of AllAlpha",
		},
		{
			arg: "AllAlpha=false,TestAlpha=true,TestAlphaNoVersion=true",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          true,
				testBetaGate:           false,
				testAlphaGateNoVersion: true,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "TestAlpha=true,TestAlphaNoVersion=true,AllAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          true,
				testBetaGate:           false,
				testAlphaGateNoVersion: true,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "AllAlpha=true,TestAlpha=false,TestAlphaNoVersion=false",
			expect: map[Feature]bool{
				allAlphaGate:           true,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           true,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "TestAlpha=false,TestAlphaNoVersion=false,AllAlpha=true",
			expect: map[Feature]bool{
				allAlphaGate:           true,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           true,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "TestBeta=true,TestBetaNoVersion=true,TestGA=true,AllAlpha=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             true,
				testAlphaGate:          false,
				testBetaGate:           true,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  true,
			},
		},

		{
			arg: "AllBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "AllBeta=true",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            true,
				testGAGate:             true,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "AllBeta=banana",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
			parseError: "invalid value of AllBeta",
		},
		{
			arg: "AllBeta=false,TestBeta=true,TestBetaNoVersion=true,TestGA=true",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             true,
				testAlphaGate:          false,
				testBetaGate:           true,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  true,
			},
		},
		{
			arg: "TestBeta=true,TestBetaNoVersion=true,AllBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           true,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  true,
			},
		},
		{
			arg: "AllBeta=true,TestBetaNoVersion=false,TestBeta=false,TestGA=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            true,
				testGAGate:             false,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "TestBeta=false,TestBetaNoVersion=false,AllBeta=true",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            true,
				testGAGate:             true,
				testAlphaGate:          false,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
		{
			arg: "TestAlpha=true,AllBeta=false",
			expect: map[Feature]bool{
				allAlphaGate:           false,
				allBetaGate:            false,
				testGAGate:             false,
				testAlphaGate:          true,
				testBetaGate:           false,
				testAlphaGateNoVersion: false,
				testBetaGateNoVersion:  false,
			},
		},
	}
	for i, test := range tests {
		t.Run(test.arg, func(t *testing.T) {
			fs := pflag.NewFlagSet("testfeaturegateflag", pflag.ContinueOnError)
			f := NewFeatureGateForTest("1.29")
			f.SetCompatibilityVersion("1.28")

			f.AddVersioned(map[Feature]VersionedSpecs{
				testGAGate: VersionedSpecs{
					{Version: mustParseVersion("1.29"), Default: true, PreRelease: GA},
					{Version: mustParseVersion("1.28"), Default: false, PreRelease: Beta},
					{Version: mustParseVersion("1.27"), Default: false, PreRelease: Alpha},
				},
				testAlphaGate: VersionedSpecs{
					{Version: mustParseVersion("1.29"), Default: false, PreRelease: Alpha},
				},
				testBetaGate: VersionedSpecs{
					{Version: mustParseVersion("1.29"), Default: false, PreRelease: Beta},
					{Version: mustParseVersion("1.28"), Default: false, PreRelease: Alpha},
				},
			})
			f.Add(map[Feature]FeatureSpec{
				testAlphaGateNoVersion: {Default: false, PreRelease: Alpha},
				testBetaGateNoVersion:  {Default: false, PreRelease: Beta},
			})
			f.AddFlag(fs)

			err := fs.Parse([]string{fmt.Sprintf("--%s=%s", flagName, test.arg)})
			if test.parseError != "" {
				if !strings.Contains(err.Error(), test.parseError) {
					t.Errorf("%d: Parse() Expected %v, Got %v", i, test.parseError, err)
				}
			} else if err != nil {
				t.Errorf("%d: Parse() Expected nil, Got %v", i, err)
			}
			for k, v := range test.expect {
				if actual := f.enabled.Load().(map[Feature]bool)[k]; actual != v {
					t.Errorf("%d: expected %s=%v, Got %v", i, k, v, actual)
				}
			}
		})
	}
}

func TestVersionedFeatureGateOverride(t *testing.T) {
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"

	// Don't parse the flag, assert defaults are used.
	var f *featureGate = NewFeatureGateForTest("1.29")
	f.SetCompatibilityVersion("1.28")
	f.AddVersioned(map[Feature]VersionedSpecs{
		testAlphaGate: VersionedSpecs{
			{Version: mustParseVersion("1.29"), Default: false, PreRelease: Alpha},
		},
		testBetaGate: VersionedSpecs{
			{Version: mustParseVersion("1.29"), Default: false, PreRelease: Beta},
			{Version: mustParseVersion("1.28"), Default: false, PreRelease: Alpha},
		},
	})

	f.Set("TestAlpha=true,TestBeta=true")
	if f.Enabled(testAlphaGate) != true {
		t.Errorf("Expected true")
	}
	if f.Enabled(testBetaGate) != true {
		t.Errorf("Expected true")
	}

	f.Set("TestAlpha=false")
	if f.Enabled(testAlphaGate) != false {
		t.Errorf("Expected false")
	}
	if f.Enabled(testBetaGate) != true {
		t.Errorf("Expected true")
	}
}

func TestVersionedFeatureGateFlagDefaults(t *testing.T) {
	// gates for testing
	const testGAGate Feature = "TestGA"
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"

	// Don't parse the flag, assert defaults are used.
	var f *featureGate = NewFeatureGateForTest("1.29")
	f.SetCompatibilityVersion("1.28")

	f.AddVersioned(map[Feature]VersionedSpecs{
		testGAGate: VersionedSpecs{
			{Version: mustParseVersion("1.29"), Default: true, PreRelease: GA},
			{Version: mustParseVersion("1.28"), Default: true, PreRelease: Alpha},
			{Version: mustParseVersion("1.27"), Default: false, PreRelease: Beta},
		},
		testAlphaGate: VersionedSpecs{
			{Version: mustParseVersion("1.29"), Default: false, PreRelease: Alpha},
		},
		testBetaGate: VersionedSpecs{
			{Version: mustParseVersion("1.29"), Default: true, PreRelease: Beta},
			{Version: mustParseVersion("1.28"), Default: false, PreRelease: Alpha},
		},
	})

	if f.Enabled(testAlphaGate) != false {
		t.Errorf("Expected false")
	}
	if f.Enabled(testBetaGate) != false {
		t.Errorf("Expected false")
	}
	if f.Enabled(testGAGate) != true {
		t.Errorf("Expected true")
	}
}

func TestVersionedFeatureGateKnownFeatures(t *testing.T) {
	// gates for testing
	const (
		testAlphaGate               Feature = "TestAlpha"
		testBetaGate                Feature = "TestBeta"
		testGAGate                  Feature = "TestGA"
		testDeprecatedGate          Feature = "TestDeprecated"
		testGAGateNoVersion         Feature = "TestGANoVersion"
		testAlphaGateNoVersion      Feature = "TestAlphaNoVersion"
		testBetaGateNoVersion       Feature = "TestBetaNoVersion"
		testDeprecatedGateNoVersion Feature = "TestDeprecatedNoVersion"
	)

	// Don't parse the flag, assert defaults are used.
	var f *featureGate = NewFeatureGateForTest("1.29")
	f.SetCompatibilityVersion("1.28")
	f.AddVersioned(map[Feature]VersionedSpecs{
		testGAGate: VersionedSpecs{
			{Version: mustParseVersion("1.27"), Default: false, PreRelease: Beta},
			{Version: mustParseVersion("1.28"), Default: true, PreRelease: GA},
		},
		testAlphaGate: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: false, PreRelease: Alpha},
		},
		testBetaGate: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: false, PreRelease: Beta},
		},
		testDeprecatedGate: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: true, PreRelease: Deprecated},
			{Version: mustParseVersion("1.26"), Default: false, PreRelease: Alpha},
		},
	})
	f.Add(map[Feature]FeatureSpec{
		testAlphaGateNoVersion:      {Default: false, PreRelease: Alpha},
		testBetaGateNoVersion:       {Default: false, PreRelease: Beta},
		testGAGateNoVersion:         {Default: false, PreRelease: GA},
		testDeprecatedGateNoVersion: {Default: false, PreRelease: Deprecated},
	})

	known := strings.Join(f.KnownFeatures(), " ")

	assert.Contains(t, known, testAlphaGate)
	assert.Contains(t, known, testBetaGate)
	assert.NotContains(t, known, testGAGate)
	assert.NotContains(t, known, testDeprecatedGate)
	assert.NotContains(t, known, testAlphaGateNoVersion)
	assert.NotContains(t, known, testBetaGateNoVersion)
	assert.NotContains(t, known, testGAGateNoVersion)
	assert.NotContains(t, known, testDeprecatedGateNoVersion)
}

func TestVersionedFeatureGateMetrics(t *testing.T) {
	// gates for testing
	featuremetrics.ResetFeatureInfoMetric()
	const testAlphaGate Feature = "TestAlpha"
	const testBetaGate Feature = "TestBeta"
	const testAlphaEnabled Feature = "TestAlphaEnabled"
	const testBetaDisabled Feature = "TestBetaDisabled"
	testedMetrics := []string{"kubernetes_feature_enabled"}
	expectedOutput := `
		# HELP kubernetes_feature_enabled [BETA] This metric records the data about the stage and enablement of a k8s feature.
        # TYPE kubernetes_feature_enabled gauge
        kubernetes_feature_enabled{name="TestAlpha",stage="ALPHA"} 0
        kubernetes_feature_enabled{name="TestBeta",stage="BETA"} 1
		kubernetes_feature_enabled{name="TestAlphaEnabled",stage="ALPHA"} 1
        kubernetes_feature_enabled{name="AllAlpha",stage="ALPHA"} 0
        kubernetes_feature_enabled{name="AllBeta",stage="BETA"} 0
		kubernetes_feature_enabled{name="TestBetaDisabled",stage="BETA"} 0
`

	f := NewFeatureGateForTest("1.29")
	f.SetCompatibilityVersion("1.28")
	f.AddVersioned(map[Feature]VersionedSpecs{
		testAlphaGate: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: false, PreRelease: Alpha},
			{Version: mustParseVersion("1.29"), Default: true, PreRelease: Beta},
		},
		testAlphaEnabled: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: false, PreRelease: Alpha},
			{Version: mustParseVersion("1.29"), Default: true, PreRelease: Beta},
		},
		testBetaGate: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: true, PreRelease: Beta},
			{Version: mustParseVersion("1.27"), Default: false, PreRelease: Alpha},
		},
		testBetaDisabled: VersionedSpecs{
			{Version: mustParseVersion("1.28"), Default: true, PreRelease: Beta},
			{Version: mustParseVersion("1.27"), Default: false, PreRelease: Alpha},
		},
	})

	f.SetFromMap(map[string]bool{"TestAlphaEnabled": true, "TestBetaDisabled": false})
	f.AddMetrics()
	if err := testutil.GatherAndCompare(legacyregistry.DefaultGatherer, strings.NewReader(expectedOutput), testedMetrics...); err != nil {
		t.Fatal(err)
	}
}

func TestGetCurrentVersion(t *testing.T) {
	specs := VersionedSpecs{{Version: mustParseVersion("1.29"), Default: true, PreRelease: GA},
		{Version: mustParseVersion("1.28"), Default: false, PreRelease: Beta},
		{Version: mustParseVersion("1.25"), Default: false, PreRelease: Alpha},
	}
	sort.Sort(specs)
	tests := []struct {
		cVersion string
		expect   FeatureSpec
	}{
		{
			cVersion: "1.30",
			expect:   FeatureSpec{Version: mustParseVersion("1.29"), Default: true, PreRelease: GA},
		},
		{
			cVersion: "1.29",
			expect:   FeatureSpec{Version: mustParseVersion("1.29"), Default: true, PreRelease: GA},
		},
		{
			cVersion: "1.28",
			expect:   FeatureSpec{Version: mustParseVersion("1.28"), Default: false, PreRelease: Beta},
		},
		{
			cVersion: "1.27",
			expect:   FeatureSpec{Version: mustParseVersion("1.25"), Default: false, PreRelease: Alpha},
		},
		{
			cVersion: "1.25",
			expect:   FeatureSpec{Version: mustParseVersion("1.25"), Default: false, PreRelease: Alpha},
		},
		{
			cVersion: "1.24",
			expect:   FeatureSpec{Default: false, PreRelease: preAlpha},
		},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("getCurrentVersion for compatibilityVersion %s", test.cVersion), func(t *testing.T) {
			result := getCurrentVersion(specs, mustParseVersion(test.cVersion))
			if !reflect.DeepEqual(result, test.expect) {
				t.Errorf("%d: getCurrentVersion(, %s) Expected %v, Got %v", i, test.cVersion, test.expect, result)
			}
		})
	}
}
