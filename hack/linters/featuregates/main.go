package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"
	"k8s.io/hack/linters/featuregates/pkg"
	_ "k8s.io/kubernetes/pkg/features"
)

// example usage: go run featuregates/main.go ../../pkg/features

func main() {
	singlechecker.Main(pkg.FeatureGatesAnalyzer)
}
