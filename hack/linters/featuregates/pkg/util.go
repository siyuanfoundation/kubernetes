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
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var (
	// env configs
	GOOS string = findGOOS()
)

func findGOOS() string {
	goCmd := exec.Command("go", "env", "GOOS")
	out, err := goCmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("running `go env` failed: %v\n\n%s", err, string(out)))
	}
	if len(out) == 0 {
		panic("empty result from `go env GOOS`")
	}
	return string(out)
}

// identifierName returns the name of an identifier.
// if ignorePkg, only return the last part of the identifierName.
func identifierName(v ast.Expr, ignorePkg bool) string {
	if id, ok := v.(*ast.Ident); ok {
		return id.Name
	}
	if se, ok := v.(*ast.SelectorExpr); ok {
		if ignorePkg {
			return identifierName(se.Sel, ignorePkg)
		}
		return identifierName(se.X, ignorePkg) + "." + identifierName(se.Sel, ignorePkg)
	}
	return ""
}

// importAliasMap returns the mapping from pkg path to import alias.
func importAliasMap(imports []*ast.ImportSpec) map[string]string {
	m := map[string]string{}
	for _, im := range imports {
		var importAlias string
		if im.Name == nil {
			pathSegments := strings.Split(im.Path.Value, "/")
			importAlias = strings.Trim(pathSegments[len(pathSegments)-1], "\"")
		} else {
			importAlias = im.Name.String()
		}
		m[im.Path.Value] = importAlias
	}
	return m
}

func basicStringLiteral(v ast.Expr) (string, error) {
	bl, ok := v.(*ast.BasicLit)
	if !ok {
		return "", fmt.Errorf("cannot parse a non BasicLit to basicStringLiteral")
	}

	if bl.Kind != token.STRING {
		return "", fmt.Errorf("cannot parse a non STRING token to basicStringLiteral")
	}
	return strings.Trim(bl.Value, `"`), nil
}

func basicIntLiteral(v ast.Expr) (int64, error) {
	bl, ok := v.(*ast.BasicLit)
	if !ok {
		return 0, fmt.Errorf("cannot parse a non BasicLit to basicIntLiteral")
	}

	if bl.Kind != token.INT {
		return 0, fmt.Errorf("cannot parse a non INT token to basicIntLiteral")
	}
	value, err := strconv.ParseInt(bl.Value, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parseBool(variables map[string]ast.Expr, v ast.Expr) (bool, error) {
	ident := identifierName(v, false)
	switch ident {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		if varVal, ok := variables[ident]; ok {
			return parseBool(variables, varVal)
		}
		return false, fmt.Errorf("cannot parse %s into bool", ident)
	}
}

func globalVariableDeclarations(tree *ast.File) map[string]ast.Expr {
	consts := make(map[string]ast.Expr)
	for _, d := range tree.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && (gd.Tok == token.CONST || gd.Tok == token.VAR) {
			for _, spec := range gd.Specs {
				if vspec, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vspec.Names {
						for _, value := range vspec.Values {
							consts[name.Name] = value
						}
					}
				}
			}
		}
	}
	return consts
}

func findPkgDir(pkg string) (string, error) {
	// Use Go's module mechanism.
	cmd := exec.Command("go", "list", "-find", "-json=Dir", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("running `go list` failed: %w\n\n%s", err, string(out))
	}
	result := struct {
		Dir string
	}{}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("json unmarshal of `go list` failed: %w", err)
	}
	if result.Dir != "" {
		return result.Dir, nil
	}

	return "", fmt.Errorf("empty respose from `go list`")
}

func importedGlobalVariableDeclaration(localVariables map[string]ast.Expr, imports []*ast.ImportSpec) (map[string]ast.Expr, error) {
	errs := []error{}
	for _, im := range imports {
		// get imported label
		var importAlias string
		if im.Name == nil {
			pathSegments := strings.Split(im.Path.Value, "/")
			importAlias = strings.Trim(pathSegments[len(pathSegments)-1], "\"")
		} else {
			importAlias = im.Name.String()
		}

		// find local path on disk for listed import
		pkg, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			errs = append(errs, fmt.Errorf("can't handle import '%s': %w", im.Path.Value, err))
			continue
		}
		importDirectory, err := findPkgDir(pkg)
		if err != nil {
			errs = append(errs, fmt.Errorf("can't find import '%s': %w", im.Path.Value, err))
			continue
		}

		files, err := os.ReadDir(importDirectory)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to read import directory %s: %w", importDirectory, err))
			continue
		}

		for _, file := range files {
			if file.IsDir() {
				// do not grab constants from subpackages
				continue
			}

			if strings.Contains(file.Name(), "_test") {
				// do not parse test files
				continue
			}

			if !strings.HasSuffix(file.Name(), ".go") {
				// not a go code file, do not attempt to parse
				continue
			}

			fileset := token.NewFileSet()
			tree, err := parser.ParseFile(fileset, strings.Join([]string{importDirectory, file.Name()}, string(os.PathSeparator)), nil, parser.AllErrors)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to parse path %s with error %w", im.Path.Value, err))
				continue
			}

			// pass parsed filepath into globalVariableDeclarations
			variables := globalVariableDeclarations(tree)

			// add returned map into supplied map and prepend import label to all keys
			for k, v := range variables {
				importK := strings.Join([]string{importAlias, k}, ".")
				if _, ok := localVariables[importK]; !ok {
					localVariables[importK] = v
				} else if strings.Contains(file.Name(), GOOS) {
					// cross-platform file that gets included in the correct OS build via OS build tags
					// use whatever matches GOOS
					// assume at some point we will find the correct OS version of this file
					// if we are running on an OS that does not have an OS specific file for something then we will include a constant we shouldn't
					// TODO: should we include/exclude based on the build tags?
					localVariables[importK] = v
				}
			}
		}

	}

	return localVariables, aggregate(errs)
}

type aggregate []error

func (agg aggregate) Error() string {
	msgs := []string{}
	for _, err := range agg {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, ",")
}
