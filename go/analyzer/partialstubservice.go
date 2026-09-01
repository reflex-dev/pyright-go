/*
 * partialstubservice.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * A service that maps partial stub packages into the original directory of the
 * installed library.
 *
 * Transliterated from partialStubService.ts (pyright 1.1.412), which lives at
 * the top of src/ rather than under analyzer/. It is here because it reaches
 * ExecutionEnvironment and pyTypedUtils, both of which are.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
)

// AllowMovingFunc is the optional `allowMoving` callback of
// processPartialStubPackages. packagePyTyped is `PyTypedInfo | undefined`.
type AllowMovingFunc func(isBundled bool, packagePyTyped *PyTypedInfo, stubPyTyped *PyTypedInfo) bool

// SupportPartialStubs corresponds to the interface of the same name.
//
// The interface declares processPartialStubPackages with three parameters,
// while PartialStubService's implementation takes a fourth. Go has no such
// slack, so the fourth is on the interface too and callers that had it
// defaulted pass nil.
type SupportPartialStubs interface {
	IsPartialStubPackagesScanned(execEnv *ExecutionEnvironment) bool
	IsPathScanned(path uri.Uri) bool
	ProcessPartialStubPackages(paths []uri.Uri, roots []uri.Uri, bundledStubPath uri.Uri, allowMoving AllowMovingFunc)
	ClearPartialStubs()
}

// PartialStubService corresponds to the class of the same name.
type PartialStubService struct {
	realFs uri.FileSystem

	// rootSearched holds the root paths processed.
	rootSearched *common.OrderedSet[string]

	// partialStubPackagePaths holds the partial stub package paths processed.
	partialStubPackagePaths *common.OrderedSet[string]

	// movedDirectories holds the disposables that clean up moved directories.
	movedDirectories []uri.Disposable
}

var _ SupportPartialStubs = (*PartialStubService)(nil)

func NewPartialStubService(realFs uri.FileSystem) *PartialStubService {
	return &PartialStubService{
		realFs:                  realFs,
		rootSearched:            common.NewOrderedSet[string](),
		partialStubPackagePaths: common.NewOrderedSet[string](),
	}
}

func (s *PartialStubService) IsPartialStubPackagesScanned(execEnv *ExecutionEnvironment) bool {
	if execEnv.Root == nil {
		return false
	}
	return s.IsPathScanned(execEnv.Root)
}

func (s *PartialStubService) IsPathScanned(u uri.Uri) bool {
	return s.rootSearched.Has(u.Key())
}

func (s *PartialStubService) ProcessPartialStubPackages(
	paths []uri.Uri,
	roots []uri.Uri,
	bundledStubPath uri.Uri,
	allowMoving AllowMovingFunc,
) {
	allowMovingFn := allowMoving
	if allowMovingFn == nil {
		allowMovingFn = s.allowMoving
	}

	for _, path := range paths {
		s.rootSearched.Add(path.Key())

		if !s.realFs.ExistsSync(path) || !uri.IsDirectory(s.realFs, path) {
			continue
		}

		// The original catches here and leaves an empty set of dir entries to
		// process.
		dirEntries, _ := s.realFs.ReaddirEntriesSync(path)

		isBundledStub := bundledStubPath != nil && path.Equals(bundledStubPath)
		for _, entry := range dirEntries {
			partialStubPackagePath := path.CombinePaths(entry.Name())
			isDirectory := entry.IsDirectory()
			if entry.IsSymbolicLink() {
				stat, ok := uri.TryStat(s.realFs, partialStubPackagePath)
				isDirectory = ok && stat.IsDirectory()
			}

			if !isDirectory || !strings.HasSuffix(entry.Name(), common.StubsSuffix) {
				continue
			}

			pyTypedInfo := GetPyTypedInfo(s.realFs, partialStubPackagePath)
			if pyTypedInfo == nil || !pyTypedInfo.IsPartiallyTyped {
				// Stub-Package is fully typed.
				continue
			}

			// We found partially typed stub-packages.
			s.partialStubPackagePaths.Add(partialStubPackagePath.Key())

			// Search the root to see whether we have a matching package
			// installed.
			packageName := entry.Name()[:len(entry.Name())-len(common.StubsSuffix)]
			for _, root := range roots {
				packagePath := root.CombinePaths(packageName)

				stat, ok := uri.TryStat(s.realFs, packagePath)
				if !ok || !stat.IsDirectory() {
					continue
				}

				// If the partial stub we found is from a bundled stub and the
				// library installed is marked as py.typed, ignore the bundled
				// partial stub.
				if !allowMovingFn(isBundledStub, GetPyTypedInfo(s.realFs, packagePath), pyTypedInfo) {
					continue
				}

				// Merge partial stub packages into the library.
				s.movedDirectories = append(s.movedDirectories, s.realFs.MapDirectory(
					packagePath,
					partialStubPackagePath,
					func(u uri.Uri, fs uri.FileSystem) bool {
						if u.HasExtension(".pyi") {
							return true
						}
						if !fs.ExistsSync(u) {
							return false
						}
						stat, err := fs.StatSync(u)
						return err == nil && stat.IsDirectory()
					},
				))
			}
		}
	}
}

func (s *PartialStubService) ClearPartialStubs() {
	s.rootSearched.Clear()
	s.partialStubPackagePaths.Clear()
	for _, d := range s.movedDirectories {
		d.Dispose()
	}
	s.movedDirectories = nil
}

func (s *PartialStubService) allowMoving(isBundled bool, packagePyTyped *PyTypedInfo, stubPyTyped *PyTypedInfo) bool {
	if !isBundled {
		return true
	}

	// If the partial stub we found is from a bundled stub and the library
	// installed is marked as py.typed, allow moving only if the package is
	// marked as partially typed.
	return packagePyTyped == nil || packagePyTyped.IsPartiallyTyped
}

// NoOpPartialStubs corresponds to the class of the same name.
//
// The original's comment: a no-op implementation of SupportPartialStubs for
// testing scenarios that don't require partial stub package resolution. This
// avoids the expensive file system scanning overhead of
// PartialStubService.processPartialStubPackages.
type NoOpPartialStubs struct{}

var _ SupportPartialStubs = (*NoOpPartialStubs)(nil)

// IsPartialStubPackagesScanned always reports as already scanned, to prevent
// actual scanning.
func (s *NoOpPartialStubs) IsPartialStubPackagesScanned(execEnv *ExecutionEnvironment) bool {
	return true
}

func (s *NoOpPartialStubs) IsPathScanned(path uri.Uri) bool { return true }

func (s *NoOpPartialStubs) ProcessPartialStubPackages(paths []uri.Uri, roots []uri.Uri, bundledStubPath uri.Uri, allowMoving AllowMovingFunc) {
}

func (s *NoOpPartialStubs) ClearPartialStubs() {}
