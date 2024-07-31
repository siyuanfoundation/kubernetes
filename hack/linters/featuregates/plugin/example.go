// This must be package main
package main

import (
	"fmt"

	"golang.org/x/tools/go/analysis"
	"k8s.io/hack/linters/featuregates/pkg"
)

func New(conf any) ([]*analysis.Analyzer, error) {
	// TODO: This must be implemented

	fmt.Printf("sizhangDebug: FeatureGatesAnalyzer configuration (%[1]T): %#[1]v\n", conf)

	// The configuration type will be map[string]any or []interface, it depends on your configuration.
	// You can use https://github.com/go-viper/mapstructure to convert map to struct.

	return []*analysis.Analyzer{pkg.FeatureGatesAnalyzer}, nil
}
