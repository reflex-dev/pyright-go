/*
 * typeshedinfoprovider.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeshedInfoProvider.ts (pyright 1.1.412).
 */

package analyzer

import (
	"sort"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// CreateDefaultTypeshedInfoProvider corresponds to the function of the same
// name.
func CreateDefaultTypeshedInfoProvider(fileSystem ImportResolverFileSystem) TypeshedInfoProvider {
	return &defaultTypeshedInfoProvider{
		fileSystem:                fileSystem,
		typeshedRootCache:         map[string]uri.Uri{},
		typeshedSubdirectoryCache: map[string]uri.Uri{},
		thirdPartyPackageMapCache: map[string]TypeshedThirdPartyPackageMapResult{},
		stdlibVersionInfoCache:    map[string]*common.OrderedMap[string, SupportedVersionInfo]{},
	}
}

type defaultTypeshedInfoProvider struct {
	fileSystem ImportResolverFileSystem

	// The first two caches store `Uri | undefined` and are read with
	// `cached !== undefined`, so a cached *absence* is indistinguishable from a
	// miss and gets recomputed every time. A nil Uri here has exactly that
	// property, so the behaviour carries over without needing a comment at each
	// use.
	typeshedRootCache         map[string]uri.Uri
	typeshedSubdirectoryCache map[string]uri.Uri

	thirdPartyPackageMapCache map[string]TypeshedThirdPartyPackageMapResult
	stdlibVersionInfoCache    map[string]*common.OrderedMap[string, SupportedVersionInfo]
}

// customTypeshedKey is `customTypeshedPath?.key ?? ”`.
func customTypeshedKey(customTypeshedPath uri.Uri) string {
	if customTypeshedPath == nil {
		return ""
	}
	return customTypeshedPath.Key()
}

func (p *defaultTypeshedInfoProvider) GetTypeshedRoot(customTypeshedPath uri.Uri, importLogger *ImportLogger) uri.Uri {
	key := customTypeshedKey(customTypeshedPath)
	if cached := p.typeshedRootCache[key]; cached != nil {
		return cached
	}

	root := p.computeTypeshedRoot(customTypeshedPath)
	p.typeshedRootCache[key] = root
	return root
}

func (p *defaultTypeshedInfoProvider) GetTypeshedSubdirectory(
	isStdLib bool,
	customTypeshedPath uri.Uri,
	importLogger *ImportLogger,
) uri.Uri {
	kind := "thirdParty"
	if isStdLib {
		kind = "stdlib"
	}
	key := kind + ":" + customTypeshedKey(customTypeshedPath)
	if cached := p.typeshedSubdirectoryCache[key]; cached != nil {
		return cached
	}

	typeshedRoot := p.GetTypeshedRoot(customTypeshedPath, importLogger)
	if typeshedRoot == nil {
		p.typeshedSubdirectoryCache[key] = nil
		return nil
	}

	subdir := GetTypeshedSubdirectory(typeshedRoot, isStdLib)
	if !p.fileSystem.DirExists(subdir) {
		p.typeshedSubdirectoryCache[key] = nil
		return nil
	}

	p.typeshedSubdirectoryCache[key] = subdir
	return subdir
}

func (p *defaultTypeshedInfoProvider) GetThirdPartyPackageMap(
	customTypeshedPath uri.Uri,
	importLogger *ImportLogger,
) TypeshedThirdPartyPackageMapResult {
	key := customTypeshedKey(customTypeshedPath)
	if cached, ok := p.thirdPartyPackageMapCache[key]; ok {
		return cached
	}

	thirdPartyDir := p.GetTypeshedSubdirectory(false, customTypeshedPath, importLogger)
	typeshedThirdPartyPackagePaths := common.NewOrderedMap[string, []uri.Uri]()

	if thirdPartyDir != nil {
		// The original's comment: readdirEntriesSync is cached by
		// ImportResolverFileSystem, so repeated calls across ImportResolvers
		// will share the same cached directory enumerations.
		outerEntries, _ := p.fileSystem.ReaddirEntriesSync(thirdPartyDir)
		for _, outerEntry := range outerEntries {
			if !outerEntry.IsDirectory() {
				continue
			}

			innerDirPath := thirdPartyDir.CombinePaths(outerEntry.Name())

			innerEntries, _ := p.fileSystem.ReaddirEntriesSync(innerDirPath)
			for _, innerEntry := range innerEntries {
				if innerEntry.Name() == "@python2" {
					continue
				}

				if innerEntry.IsDirectory() {
					addTypeshedPackagePath(typeshedThirdPartyPackagePaths, innerEntry.Name(), innerDirPath)
				} else if innerEntry.IsFile() {
					if strings.HasSuffix(innerEntry.Name(), ".pyi") {
						strippedFileName := common.StripFileExtension(innerEntry.Name(), false)
						addTypeshedPackagePath(typeshedThirdPartyPackagePaths, strippedFileName, innerDirPath)
					}
				}
			}
		}
	}

	// `Array.from(new Set(flattenPaths)).sort()`. The Set dedupes by object
	// identity, which for interned Uris is the same as deduping by key; the
	// sort is JavaScript's default, which compares the elements' string forms.
	seen := map[uri.Uri]bool{}
	flattenPaths := []uri.Uri{}
	for _, k := range typeshedThirdPartyPackagePaths.Keys() {
		paths, _ := typeshedThirdPartyPackagePaths.Get(k)
		for _, path := range paths {
			if !seen[path] {
				seen[path] = true
				flattenPaths = append(flattenPaths, path)
			}
		}
	}
	sort.SliceStable(flattenPaths, func(i, j int) bool {
		return flattenPaths[i].String() < flattenPaths[j].String()
	})

	result := TypeshedThirdPartyPackageMapResult{
		PackagePaths: typeshedThirdPartyPackagePaths,
		Paths:        flattenPaths,
	}

	p.thirdPartyPackageMapCache[key] = result
	return result
}

func addTypeshedPackagePath(m *common.OrderedMap[string, []uri.Uri], name string, dirPath uri.Uri) {
	if pathList, ok := m.Get(name); ok {
		m.Set(name, append(pathList, dirPath))
	} else {
		m.Set(name, []uri.Uri{dirPath})
	}
}

func (p *defaultTypeshedInfoProvider) GetStdLibModuleVersionInfo(
	customTypeshedPath uri.Uri,
	importLogger *ImportLogger,
) *common.OrderedMap[string, SupportedVersionInfo] {
	key := customTypeshedKey(customTypeshedPath)
	if cached, ok := p.stdlibVersionInfoCache[key]; ok {
		return cached
	}

	versionRangeMap := common.NewOrderedMap[string, SupportedVersionInfo]()

	// Read the VERSIONS file from typeshed.
	typeshedStdLibPath := p.GetTypeshedSubdirectory(true, customTypeshedPath, importLogger)
	if typeshedStdLibPath != nil {
		versionsFilePath := typeshedStdLibPath.CombinePaths("VERSIONS")
		p.readVersionsFile(versionsFilePath, versionRangeMap, importLogger)
	}

	p.stdlibVersionInfoCache[key] = versionRangeMap
	return versionRangeMap
}

// readVersionsFile is the body of the original's try block. The two failure
// paths -- statSync throwing and readFileSync throwing -- both land in the same
// catch there, so both log the same message here.
func (p *defaultTypeshedInfoProvider) readVersionsFile(
	versionsFilePath uri.Uri,
	versionRangeMap *common.OrderedMap[string, SupportedVersionInfo],
	importLogger *ImportLogger,
) {
	fileStats, err := p.fileSystem.StatSync(versionsFilePath)
	if err != nil {
		importLogger.Log("Could not read typeshed stdlib VERSIONS file: '" + err.Error() + "'")
		return
	}

	if !(fileStats.Size() > 0 && fileStats.Size() < 256*1024) {
		importLogger.Log("Typeshed stdlib VERSIONS file is unexpectedly large")
		return
	}

	fileContents, err := p.fileSystem.ReadFileSync(versionsFilePath)
	if err != nil {
		importLogger.Log("Could not read typeshed stdlib VERSIONS file: '" + err.Error() + "'")
		return
	}

	// `fileContents.split(/\r?\n/)`.
	lines := strings.Split(strings.ReplaceAll(string(fileContents), "\r\n", "\n"), "\n")
	for _, line := range lines {
		commentSplit := strings.Split(line, "#")

		// Platform-specific information can be specified after a semicolon.
		semicolonSplit := strings.Split(commentSplit[0], ";")
		for i := range semicolonSplit {
			semicolonSplit[i] = common.TrimJSString(semicolonSplit[i])
		}

		// Version information is found after a colon.
		colonSplit := strings.Split(semicolonSplit[0], ":")
		if len(colonSplit) != 2 {
			continue
		}

		versionSplit := strings.Split(colonSplit[1], "-")
		if len(versionSplit) > 2 {
			continue
		}

		moduleName := common.TrimJSString(colonSplit[0])
		if moduleName == "" {
			continue
		}

		minVersionString := common.TrimJSString(versionSplit[0])
		if strings.HasSuffix(minVersionString, "+") {
			// If the version ends in "+", strip it off.
			minVersionString = minVersionString[:len(minVersionString)-1]
		}

		minVersion := common.PythonVersionFromString(minVersionString)
		if minVersion == nil {
			v := common.PythonVersion3_0
			minVersion = &v
		}

		var maxVersion *common.PythonVersion
		if len(versionSplit) > 1 {
			maxVersion = common.PythonVersionFromString(common.TrimJSString(versionSplit[1]))
		}

		// The original's comment: a semicolon can be followed by a
		// semicolon-delimited list of other exclusions. The "platform"
		// exclusion is a comma delimited list of platforms that are supported
		// or not supported.
		var supportedPlatforms []string
		var unsupportedPlatforms []string
		const platformsHeader = "platforms="
		platformExclusions := ""
		for _, s := range semicolonSplit[1:] {
			if strings.HasPrefix(s, platformsHeader) {
				platformExclusions = s
				break
			}
		}

		if platformExclusions != "" {
			platformExclusions = common.TrimJSString(platformExclusions)[len(platformsHeader):]
			for _, platform := range strings.Split(platformExclusions, ",") {
				platform = common.TrimJSString(platform)
				isUnsupported := false

				// Remove the '!' from the start if it's an exclusion.
				if strings.HasPrefix(platform, "!") {
					isUnsupported = true
					platform = platform[1:]
				}

				if isUnsupported {
					unsupportedPlatforms = append(unsupportedPlatforms, platform)
				} else {
					supportedPlatforms = append(supportedPlatforms, platform)
				}
			}
		}

		versionRangeMap.Set(moduleName, SupportedVersionInfo{
			Min:                  *minVersion,
			Max:                  maxVersion,
			SupportedPlatforms:   supportedPlatforms,
			UnsupportedPlatforms: unsupportedPlatforms,
		})
	}
}

// computeTypeshedRoot returns nil where the original returns undefined.
func (p *defaultTypeshedInfoProvider) computeTypeshedRoot(customTypeshedPath uri.Uri) uri.Uri {
	// Did the user specify a typeshed path? If not, use the fallback.
	if customTypeshedPath != nil {
		if p.fileSystem.DirExists(customTypeshedPath) {
			return customTypeshedPath
		}
	}

	fallback := GetTypeShedFallbackPath(p.fileSystem)
	if fallback == nil {
		fallback = uri.Empty()
	}
	if fallback.IsEmpty() {
		return nil
	}
	return fallback
}
