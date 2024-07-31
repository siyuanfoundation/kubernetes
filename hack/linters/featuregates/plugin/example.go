// This must be package main
package main

import (
	"bytes"
	"encoding/json"
	"fmt"

	"golang.org/x/tools/go/analysis"
	"k8s.io/hack/linters/featuregates/pkg"
)

type settings struct {
	Check  map[string]bool `json:"check"`
	Config string          `json:"config"`
}

func New(pluginSettings interface{}) ([]*analysis.Analyzer, error) {
	// We could manually parse the settings. This would involve several
	// type assertions. Encoding as JSON and then decoding into our
	// settings struct is easier.
	//
	// The downside is that format errors are less user-friendly.
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(pluginSettings); err != nil {
		return nil, fmt.Errorf("encoding settings as internal JSON buffer: %v", err)
	}
	var s settings
	decoder := json.NewDecoder(&buffer)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s); err != nil {
		return nil, fmt.Errorf("decoding settings from internal JSON buffer: %v", err)
	}

	fmt.Printf("sizhangDebug: FeatureGatesAnalyzer settings %v\n", s)

	// The configuration type will be map[string]any or []interface, it depends on your configuration.
	// You can use https://github.com/go-viper/mapstructure to convert map to struct.

	return []*analysis.Analyzer{pkg.FeatureGatesAnalyzer}, nil
}
