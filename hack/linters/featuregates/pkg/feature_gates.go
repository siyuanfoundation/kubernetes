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

package pkg

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
	"golang.org/x/tools/go/analysis"
	"gopkg.in/yaml.v2"

	"k8s.io/apimachinery/pkg/util/version"
)

var (
	alphabeticalOrder          bool
	k8RootPath                 = "/Users/sizhang/Github/k8s/kubernetes"
	unversionedFeatureListFile = "test/static_analysis/test_data/unversioned_feature_list.yaml"
	versionedFeatureListFile   = "test/static_analysis/test_data/versioned_feature_list.yaml"
)

const (
	featureGatePkg = "\"k8s.io/component-base/featuregate\""
)

type featureSpec struct {
	Default       bool   `yaml:"default" json:"default"`
	LockToDefault bool   `yaml:"lockToDefault" json:"lockToDefault"`
	PreRelease    string `yaml:"preRelease" json:"preRelease"`
	Version       string `yaml:"version" json:"version"`
}

type featureInfo struct {
	Name           string        `yaml:"name" json:"name"`
	FullName       string        `yaml:"-" json:"-"`
	VersionedSpecs []featureSpec `yaml:"versionedSpecs" json:"versionedSpecs"`
}

var FeatureGatesAnalyzer = &analysis.Analyzer{
	Name: "featuregatesVerify",
	Doc:  "Verify featuregates up to date",
	Run:  runVerify,
}

func runVerify(pass *analysis.Pass) (interface{}, error) {
	fmt.Printf("sizhangDebug: runVerify, len(pass.Files) = %d\n", len(pass.Files))
	for _, f := range pass.Files {
		fmt.Printf("sizhangDebug: runVerify, f = %v\n", f.Name.String())
	}
	return nil, nil
	// return verify(pass, false, unversionedFeatureListFile)
	// result, err := verify(pass, false, unversionedFeatureListFile)
	// if err != nil {
	// 	return result, err
	// }
	// return verify(pass, true, versionedFeatureListFile)
}

func verify(pass *analysis.Pass, versioned bool, featureListFile string) (interface{}, error) {
	allFeatures := []featureInfo{}
	versionedStr := "unversioned"
	if versioned {
		versionedStr = "versioned"
	}
	fmt.Printf("sizhang: len(Files) = %d\n", len(pass.Files))
	for _, file := range pass.Files {
		features, parseErr := extractFeatureInfoListFromFile(file, versioned)
		if parseErr != nil {
			return nil, parseErr
		}
		allFeatures = append(allFeatures, features...)
		fmt.Printf("File = %v\n", *file)
	}
	fmt.Printf("sizhangDebug: found %d %s features\n", len(allFeatures), versionedStr)
	err := verifyFeatureListDiff(allFeatures, featureListFile)
	if err != nil {
		pass.Reportf(0, "sizhangDebug: report %s not up to date\nfound %d %s features\n%s", featureListFile, len(allFeatures), versionedStr, err.Error())
	}
	return nil, nil
}

func verifyFeatureListFunc(cmd *cobra.Command, args []string) {
	if err := verifyOrUpdateFeatureList(k8RootPath, unversionedFeatureListFile, false, false); err != nil {
		panic(err)
	}
	if err := verifyOrUpdateFeatureList(k8RootPath, versionedFeatureListFile, false, true); err != nil {
		panic(err)
	}
}

func updateFeatureListFunc(cmd *cobra.Command, args []string) {
	if err := verifyOrUpdateFeatureList(k8RootPath, unversionedFeatureListFile, true, false); err != nil {
		panic(err)
	}
	if err := verifyOrUpdateFeatureList(k8RootPath, versionedFeatureListFile, true, true); err != nil {
		panic(err)
	}
}

// verifyOrUpdateFeatureList walks all the files under pkg/ and staging/ to find the list of all the features in
// map[featuregate.Feature]featuregate.FeatureSpec or map[featuregate.Feature]featuregate.VersionedSpecs.
// It will then update the feature list in featureListFile, or verifies there is no change from the existing list.
func verifyOrUpdateFeatureList(rootPath, featureListFile string, update, versioned bool) error {
	featureList := []featureInfo{}
	features, err := searchPathForFeatures(filepath.Join(rootPath, "pkg"), versioned)
	if err != nil {
		return err
	}
	featureList = append(featureList, features...)

	features, err = searchPathForFeatures(filepath.Join(rootPath, "staging"), versioned)
	if err != nil {
		return err
	}
	featureList = append(featureList, features...)

	sort.Slice(featureList, func(i, j int) bool {
		return strings.ToLower(featureList[i].Name) < strings.ToLower(featureList[j].Name)
	})
	featureList, err = dedupeFeatureList(featureList)
	if err != nil {
		return err
	}

	filePath := filepath.Join(rootPath, featureListFile)
	baseFeatureListBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	baseFeatureList := []featureInfo{}
	err = yaml.Unmarshal(baseFeatureListBytes, &baseFeatureList)
	if err != nil {
		return err
	}

	// only feature deletion is allowed for unversioned features.
	// all new features or feature updates should be migrated to versioned feature gate.
	// https://github.com/kubernetes/kubernetes/issues/125031
	if !versioned {
		if err := verifyFeatureDeletionOnly(featureList, baseFeatureList); err != nil {
			return err
		}
	}

	if update {
		data, err := yaml.Marshal(featureList)
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0644)
	}

	if diff := cmp.Diff(featureList, baseFeatureList); diff != "" {
		return fmt.Errorf("detected diff in unversioned feature list, diff: \n%s", diff)
	}
	return nil
}

func verifyFeatureListDiff(featureList []featureInfo, featureListFile string) error {
	sort.Slice(featureList, func(i, j int) bool {
		return strings.ToLower(featureList[i].Name) < strings.ToLower(featureList[j].Name)
	})
	featureList, err := dedupeFeatureList(featureList)
	if err != nil {
		return err
	}
	// rootPath, err := filepath.Abs(".")
	// if err != nil {
	// 	return err
	// }
	filePath := filepath.Join(k8RootPath, featureListFile)
	baseFeatureListBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	baseFeatureList := []featureInfo{}
	err = yaml.Unmarshal(baseFeatureListBytes, &baseFeatureList)
	if err != nil {
		return err
	}

	if diff := cmp.Diff(featureList, baseFeatureList); diff != "" {
		return fmt.Errorf("detected diff in feature list, diff: \n%s", diff)
	}
	return nil
}

func dedupeFeatureList(featureList []featureInfo) ([]featureInfo, error) {
	if featureList == nil || len(featureList) < 1 {
		return featureList, nil
	}
	last := featureList[0]
	// clean up FullName field for the final output
	last.FullName = ""
	deduped := []featureInfo{last}
	for i := 1; i < len(featureList); i++ {
		f := featureList[i]
		if f.Name == last.Name {
			// if it is a duplicate feature, verify the lifecycles are the same
			if !reflect.DeepEqual(last.VersionedSpecs, f.VersionedSpecs) {
				return deduped, fmt.Errorf("multiple conflicting specs found for feature:%s, [\n%v, \n%v]", last.Name, last.VersionedSpecs, f.VersionedSpecs)
			}
			continue
		}
		last = f
		last.FullName = ""
		deduped = append(deduped, last)

	}
	return deduped, nil
}

func verifyFeatureDeletionOnly(newFeatureList []featureInfo, oldFeatureList []featureInfo) error {
	oldFeatureSet := make(map[string]*featureInfo)
	for _, f := range oldFeatureList {
		oldFeatureSet[f.Name] = &f
	}
	newFeatures := []string{}
	for _, f := range newFeatureList {
		oldSpecs, found := oldFeatureSet[f.Name]
		if !found {
			newFeatures = append(newFeatures, f.Name)
		} else if !reflect.DeepEqual(*oldSpecs, f) {
			return fmt.Errorf("feature %s changed with diff: %s", f.Name, cmp.Diff(*oldSpecs, f))
		}
	}
	if len(newFeatures) > 0 {
		return fmt.Errorf("new features added to FeatureSpec map! %v\nPlease add new features through VersionedSpecs map ONLY! ", newFeatures)
	}
	return nil
}

func searchPathForFeatures(path string, versioned bool) ([]featureInfo, error) {
	allFeatures := []featureInfo{}
	// Create a FileSet to work with
	fset := token.NewFileSet()
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if strings.HasPrefix(path, "vendor") || strings.HasPrefix(path, "_") {
			return filepath.SkipDir
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		features, parseErr := extractFeatureInfoListFromFilePath(fset, path, versioned)
		if parseErr != nil {
			return parseErr
		}
		allFeatures = append(allFeatures, features...)
		return nil
	})
	return allFeatures, err
}

func extractFeatureInfoListFromFilePath(fset *token.FileSet, filePath string, versioned bool) (allFeatures []featureInfo, err error) {
	// Parse the file and create an AST
	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return allFeatures, err
	}
	file, err := parser.ParseFile(fset, absFilePath, nil, parser.AllErrors)
	if err != nil {
		return allFeatures, err
	}
	return extractFeatureInfoListFromFile(file, versioned)
}

// extractFeatureInfoListFromFile extracts info all the the features from
// map[featuregate.Feature]featuregate.FeatureSpec or map[featuregate.Feature]featuregate.VersionedSpecs from the given file.
func extractFeatureInfoListFromFile(file *ast.File, versioned bool) (allFeatures []featureInfo, err error) {

	aliasMap := importAliasMap(file.Imports)
	// any file containing features should have imported the featuregate pkg.
	if _, ok := aliasMap[featureGatePkg]; !ok {
		return allFeatures, err
	}
	// ignore tests
	if _, ok := aliasMap["testing"]; ok {
		return allFeatures, err
	}
	variables := globalVariableDeclarations(file)
	// use importedGlobalVariableDeclaration as best effort, ignore errors.
	// variables, _ = importedGlobalVariableDeclaration(variables, file.Imports)

	for _, d := range file.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && (gd.Tok == token.CONST || gd.Tok == token.VAR) {
			for _, spec := range gd.Specs {
				if vspec, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vspec.Names {
						for _, value := range vspec.Values {
							features, err := extractFeatureInfoList(file.Name.String(), value, aliasMap, variables, versioned)
							if err != nil {
								return allFeatures, err
							}
							if len(features) > 0 {
								fmt.Printf("found %d features in FeatureSpecMap var %s in file: %s\n", len(features), name, file.Name.String())
								allFeatures = append(allFeatures, features...)
							}
						}
					}
				}
			}
		}
		if fd, ok := d.(*ast.FuncDecl); ok {
			for _, stmt := range fd.Body.List {
				if st, ok := stmt.(*ast.ReturnStmt); ok {
					for _, value := range st.Results {
						features, err := extractFeatureInfoList(file.Name.String(), value, aliasMap, variables, versioned)
						if err != nil {
							return allFeatures, err
						}
						if len(features) > 0 {
							fmt.Printf("found %d features in FeatureSpecMap of func %s in file: %s\n", len(features), fd.Name, file.Name.String())
							allFeatures = append(allFeatures, features...)
						}
					}
				}
			}
		}
	}
	return
}

func getPkgPrefix(s string) string {
	if strings.Contains(s, ".") {
		return strings.Split(s, ".")[0]
	}
	return ""
}

func verifyAlphabeticOrder(keys []string, path string) error {
	keysSorted := make([]string, len(keys))
	copy(keysSorted, keys)
	sort.Slice(keysSorted, func(i, j int) bool {
		keyI := strings.ToLower(keysSorted[i])
		keyJ := strings.ToLower(keysSorted[j])
		if getPkgPrefix(keyI) == getPkgPrefix(keyJ) {
			return keyI < keyJ
		}
		return getPkgPrefix(keyI) < getPkgPrefix(keyJ)
	})
	if diff := cmp.Diff(keys, keysSorted); diff != "" {
		return fmt.Errorf("features in %s are not in alphabetic order, diff: %s", path, diff)
	}
	return nil
}

// extractFeatureInfoList extracts the info all the the features from
// map[featuregate.Feature]featuregate.FeatureSpec or map[featuregate.Feature]featuregate.VersionedSpecs.
func extractFeatureInfoList(filePath string, v ast.Expr, aliasMap map[string]string, variables map[string]ast.Expr, versioned bool) ([]featureInfo, error) {
	keys := []string{}
	features := []featureInfo{}
	cl, ok := v.(*ast.CompositeLit)
	if !ok {
		return features, nil
	}
	mt, ok := cl.Type.(*ast.MapType)
	if !ok {
		return features, nil
	}
	if !isFeatureSpecType(mt.Value, aliasMap, versioned) {
		return features, nil
	}
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		info, err := parseFeatureInfo(variables, kv, versioned)
		if err != nil {
			return features, err
		}
		features = append(features, info)
		keys = append(keys, info.FullName)
	}
	if alphabeticalOrder {
		// verifies the features are sorted in the map
		if err := verifyAlphabeticOrder(keys, filePath); err != nil {
			return features, err
		}
	}
	return features, nil
}

func isFeatureSpecType(v ast.Expr, aliasMap map[string]string, versioned bool) bool {
	typeName := "FeatureSpec"
	if versioned {
		typeName = "VersionedSpecs"
	}
	alias, ok := aliasMap[featureGatePkg]
	if ok {
		typeName = alias + "." + typeName
	}
	return identifierName(v, false) == typeName
}

func parseFeatureInfo(variables map[string]ast.Expr, kv *ast.KeyValueExpr, versioned bool) (featureInfo, error) {
	info := featureInfo{
		Name:           identifierName(kv.Key, true),
		FullName:       identifierName(kv.Key, false),
		VersionedSpecs: []featureSpec{},
	}
	specExps := []ast.Expr{}
	if versioned {
		if cl, ok := kv.Value.(*ast.CompositeLit); ok {
			specExps = append(specExps, cl.Elts...)
		}
	} else {
		specExps = append(specExps, kv.Value)
	}
	for _, specExp := range specExps {
		spec, err := parseFeatureSpec(variables, specExp)
		if err != nil {
			return info, err
		}
		info.VersionedSpecs = append(info.VersionedSpecs, spec)
	}
	// verify FeatureSpec in VersionedSpecs are ordered by version.
	if len(info.VersionedSpecs) > 1 {
		specsSorted := make([]featureSpec, len(info.VersionedSpecs))
		copy(specsSorted, info.VersionedSpecs)
		sort.Slice(specsSorted, func(i, j int) bool {
			verI := MustParse(specsSorted[i].Version)
			verJ := MustParse(specsSorted[j].Version)
			return verI.LessThan(verJ)
		})
		if diff := cmp.Diff(info.VersionedSpecs, specsSorted); diff != "" {
			return info, fmt.Errorf("VersionedSpecs in feature %s are not ordered by version, diff: %s", info.Name, diff)
		}
	}
	return info, nil
}

func parseFeatureSpec(variables map[string]ast.Expr, v ast.Expr) (featureSpec, error) {
	spec := featureSpec{}
	cl, ok := v.(*ast.CompositeLit)
	if !ok {
		return spec, fmt.Errorf("expect FeatureSpec to be a CompositeLit")
	}
	for _, elt := range cl.Elts {
		switch eltType := elt.(type) {
		case *ast.KeyValueExpr:
			key := identifierName(eltType.Key, true)
			switch key {
			case "Default":
				boolValue, err := parseBool(variables, eltType.Value)
				if err != nil {
					return spec, err
				}
				spec.Default = boolValue

			case "LockToDefault":
				boolValue, err := parseBool(variables, eltType.Value)
				if err != nil {
					return spec, err
				}
				spec.LockToDefault = boolValue

			case "PreRelease":
				spec.PreRelease = identifierName(eltType.Value, true)

			case "Version":
				ver, err := parseVersion(eltType.Value)
				if err != nil {
					return spec, err
				}
				spec.Version = ver
			}

		default:
			return spec, fmt.Errorf("cannot parse FeatureSpec")

		}
	}
	return spec, nil
}

func parseVersion(v ast.Expr) (string, error) {
	fc, ok := v.(*ast.CallExpr)
	if !ok {
		return "", fmt.Errorf("expect FeatureSpec Version to be a function call")
	}
	funcName := identifierName(fc.Fun, true)
	switch funcName {
	case "MustParse":
		return basicStringLiteral(fc.Args[0])

	case "MajorMinor":
		major, err := basicIntLiteral(fc.Args[0])
		if err != nil {
			return "", err
		}
		minor, err := basicIntLiteral(fc.Args[1])
		return fmt.Sprintf("%d.%d", major, minor), err

	default:
		return "", fmt.Errorf("unrecognized function call in FeatureSpec Version")
	}
}

func MustParse(str string) *version.Version {
	v, err := version.ParseGeneric(str)
	if err != nil {
		panic(err)
	}
	return v
}
