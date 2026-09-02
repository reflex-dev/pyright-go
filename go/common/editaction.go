/*
 * editaction.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Represents a single edit within a file.
 *
 * Transliterated from common/editAction.ts (pyright 1.1.412).
 *
 * The Uri these carry lives in common/uri, which imports this package's
 * siblings, so FileEditAction is declared in terms of the UriLike subset the
 * type actually uses -- see the note in common/uri/filesystem.go for why the
 * two packages cannot merge.
 */

package common

// TextEditAction corresponds to the interface of the same name.
type TextEditAction struct {
	Range           Range
	ReplacementText string
}

// EditActionUri is the part of the Uri interface an edit action needs.
type EditActionUri interface {
	Key() string
	String() string
}

// FileEditAction corresponds to the interface of the same name.
type FileEditAction struct {
	TextEditAction

	FileUri EditActionUri
}

// FileOperationKind corresponds to the `kind` discriminant of FileOperation.
type FileOperationKind = string

const (
	FileOperationCreate FileOperationKind = "create"
	FileOperationDelete FileOperationKind = "delete"
	FileOperationRename FileOperationKind = "rename"
)

// FileOperation corresponds to the union of RenameFileOperation,
// CreateFileOperation and DeleteFileOperation. The three share a discriminant
// and between them use three fields, so they are one struct here rather than
// three types and an interface.
type FileOperation struct {
	Kind FileOperationKind

	// FileUri is set for create and delete.
	FileUri EditActionUri

	// OldFileUri and NewFileUri are set for rename.
	OldFileUri EditActionUri
	NewFileUri EditActionUri
}

// FileEditActions corresponds to the interface of the same name.
type FileEditActions struct {
	Edits          []FileEditAction
	FileOperations []FileOperation
}

// FileEditActionsAreEqual corresponds to FileEditAction.areEqual.
func FileEditActionsAreEqual(e1 FileEditAction, e2 FileEditAction) bool {
	return e1.FileUri.Key() == e2.FileUri.Key() &&
		RangesAreEqual(e1.Range, e2.Range) &&
		e1.ReplacementText == e2.ReplacementText
}
