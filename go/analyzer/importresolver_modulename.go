/*
 * importresolver_modulename.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * getModuleNameForImport and its helper from analyzer/importResolver.ts
 * (pyright 1.1.412) -- the inverse of resolveImport. See importresolver.go for
 * how the file is split.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common/uri"
)

// GetModuleNameForImport returns the module name (of the form X.Y.Z) that needs
// to be imported from the current context to access the module with the
// specified file path. The original's comment: in a sense, it's performing the
// inverse of resolveImport.
//
// The TypeScript defaults allowInvalidModuleName and detectPyTyped to false.
func (r *ImportResolver) GetModuleNameForImport(
	fileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	allowInvalidModuleName bool,
	detectPyTyped bool,
) ModuleImportInfo {
	// Cache results of the reverse of resolveImport as we cache resolveImport.
	cacheKey := execEnvCacheKey(execEnv)
	cache, ok := r.cachedModuleNameResults[cacheKey]
	if !ok {
		cache = map[string]ModuleImportInfo{}
		r.cachedModuleNameResults[cacheKey] = cache
	}

	key := boolArg(allowInvalidModuleName) + "." + boolArg(detectPyTyped) + "." + fileUri.Key()
	if cached, ok := cache[key]; ok {
		return cached
	}

	result := r.getModuleNameForImport(fileUri, execEnv, allowInvalidModuleName, detectPyTyped)
	cache[key] = result
	return result
}

// boolArg renders a boolean the way JavaScript's template literal does.
func boolArg(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func (r *ImportResolver) getModuleNameForImport(
	fileUri uri.Uri,
	execEnv *ExecutionEnvironment,
	allowInvalidModuleName bool,
	detectPyTyped bool,
) ModuleImportInfo {
	// moduleName is `string | undefined` and the difference matters: the
	// "shortest wins" comparisons below are written `!moduleName || ...`, which
	// an empty string satisfies too, so "" is a faithful stand-in for undefined
	// here.
	moduleName := ""
	importType := ImportTypeBuiltIn
	isLocalTypingsFile := false
	isThirdPartyPyTypedPresent := false
	isTypeshedFile := false

	// The original's comment: if we cannot find a fully-qualified module name
	// with legal characters, look for one with invalid characters (e.g. "-").
	// This is important to differentiate between different modules in a project
	// in case they declare types with the same (local) name.
	moduleNameWithInvalidCharacters := ""

	// Is this a stdlib typeshed path?
	stdLibTypeshedPath := r.getStdlibTypeshedPath(
		r.configOptions.TypeshedPath, execEnv.PythonVersion, execEnv.PythonPlatform, nil, nil)

	if stdLibTypeshedPath != nil {
		moduleName = GetModuleNameFromPath(stdLibTypeshedPath, fileUri, false)
		if moduleName != "" {
			moduleDescriptor := ImportedModuleDescriptor{
				LeadingDots:     0,
				NameParts:       strings.Split(moduleName, "."),
				ImportedSymbols: nil,
			}

			if r.isStdlibTypeshedStubValidForVersion(
				moduleDescriptor, r.configOptions.TypeshedPath, execEnv.PythonVersion, execEnv.PythonPlatform, nil) {
				return ModuleImportInfo{
					ModuleNameAndType: ModuleNameAndType{
						ModuleName:         moduleName,
						ImportType:         importType,
						IsLocalTypingsFile: isLocalTypingsFile,
					},
					IsTypeshedFile:             true,
					IsThirdPartyPyTypedPresent: isThirdPartyPyTypedPresent,
				}
			}
		}
	}

	// Look for it in the root directory of the execution environment.
	if execEnv.Root != nil {
		if candidate, ok := getModuleNameInfoFromPath(execEnv.Root, fileUri, false); ok {
			if candidate.ContainsInvalidCharacters {
				moduleNameWithInvalidCharacters = candidate.ModuleName
			} else {
				moduleName = candidate.ModuleName
			}
		}

		importType = ImportTypeLocal
	}

	for _, extraPath := range execEnv.ExtraPaths {
		if candidate, ok := getModuleNameInfoFromPath(extraPath, fileUri, false); ok {
			if candidate.ContainsInvalidCharacters {
				moduleNameWithInvalidCharacters = candidate.ModuleName
			} else {
				// Does this candidate look better than the previous best module
				// name? We'll always try to use the shortest version.
				candidateModuleName := candidate.ModuleName
				if moduleName == "" || (candidateModuleName != "" && len(candidateModuleName) < len(moduleName)) {
					moduleName = candidateModuleName
					importType = ImportTypeLocal
				}
			}
		}
	}

	// Check for a typings file.
	if r.configOptions.StubPath != nil {
		if candidate, ok := getModuleNameInfoFromPath(r.configOptions.StubPath, fileUri, false); ok {
			if candidate.ContainsInvalidCharacters {
				moduleNameWithInvalidCharacters = candidate.ModuleName
			} else {
				// Does this candidate look better than the previous best module
				// name? We'll always try to use the shortest version.
				candidateModuleName := candidate.ModuleName
				if moduleName == "" || (candidateModuleName != "" && len(candidateModuleName) < len(moduleName)) {
					moduleName = candidateModuleName

					// Treat the typings path as a local import so errors are
					// reported for it.
					importType = ImportTypeLocal
					isLocalTypingsFile = true
				}
			}
		}
	}

	// Check for a typeshed file.
	thirdPartyTypeshedPath := r.getThirdPartyTypeshedPath(r.configOptions.TypeshedPath, nil)

	if thirdPartyTypeshedPath != nil {
		candidateModuleName := GetModuleNameFromPath(thirdPartyTypeshedPath, fileUri, true /* stripTopContainerDir */)

		// Does this candidate look better than the previous best module name?
		// We'll always try to use the shortest version.
		if moduleName == "" || (candidateModuleName != "" && len(candidateModuleName) < len(moduleName)) {
			moduleName = candidateModuleName
			importType = ImportTypeThirdParty
			isTypeshedFile = true
		}
	}

	thirdPartyTypeshedPathEx := r.getTypeshedPathEx(execEnv, nil)
	if thirdPartyTypeshedPathEx != nil {
		candidateModuleName := GetModuleNameFromPath(thirdPartyTypeshedPathEx, fileUri, false)

		// Does this candidate look better than the previous best module name?
		// We'll always try to use the shortest version.
		if moduleName == "" || (candidateModuleName != "" && len(candidateModuleName) < len(moduleName)) {
			moduleName = candidateModuleName
			importType = ImportTypeThirdParty
			isTypeshedFile = true
		}
	}

	// Look for the import in the list of third-party packages.
	for _, searchPath := range r.GetPythonSearchPaths(nil) {
		if candidate, ok := getModuleNameInfoFromPath(searchPath, fileUri, false); ok {
			if candidate.ContainsInvalidCharacters {
				moduleNameWithInvalidCharacters = candidate.ModuleName
			} else {
				// Does this candidate look better than the previous best module
				// name? We'll always try to use the shortest version.
				candidateModuleName := candidate.ModuleName
				if moduleName == "" || (candidateModuleName != "" && len(candidateModuleName) < len(moduleName)) {
					moduleName = candidateModuleName
					importType = ImportTypeThirdParty
					isTypeshedFile = false
				}
			}
		}
	}

	if detectPyTyped && importType == ImportTypeThirdParty {
		root := GetParentImportResolutionRoot(fileUri, execEnv.Root)

		// Go up directories one by one looking for a py.typed file.
		current := fileUri.GetDirectory()
		for r.shouldWalkUp(current, root, execEnv) {
			pyTypedInfo := r.getPyTypedInfo(current)
			if pyTypedInfo != nil {
				if !pyTypedInfo.IsPartiallyTyped {
					isThirdPartyPyTypedPresent = true
				}
				break
			}

			current = r.tryWalkUp(current)
		}
	}

	if moduleName != "" {
		return ModuleImportInfo{
			ModuleNameAndType: ModuleNameAndType{
				ModuleName:         moduleName,
				ImportType:         importType,
				IsLocalTypingsFile: isLocalTypingsFile,
			},
			IsTypeshedFile:             isTypeshedFile,
			IsThirdPartyPyTypedPresent: isThirdPartyPyTypedPresent,
		}
	}

	if allowInvalidModuleName && moduleNameWithInvalidCharacters != "" {
		return ModuleImportInfo{
			ModuleNameAndType: ModuleNameAndType{
				ModuleName:         moduleNameWithInvalidCharacters,
				ImportType:         importType,
				IsLocalTypingsFile: isLocalTypingsFile,
			},
			IsTypeshedFile:             isTypeshedFile,
			IsThirdPartyPyTypedPresent: isThirdPartyPyTypedPresent,
		}
	}

	// We didn't find any module name.
	return ModuleImportInfo{
		ModuleNameAndType: ModuleNameAndType{
			ModuleName:         "",
			ImportType:         ImportTypeLocal,
			IsLocalTypingsFile: isLocalTypingsFile,
		},
		IsTypeshedFile:             isTypeshedFile,
		IsThirdPartyPyTypedPresent: isThirdPartyPyTypedPresent,
	}
}
