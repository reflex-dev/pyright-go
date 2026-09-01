/*
 * collectionutils.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from common/collectionUtils.ts (pyright 1.1.412). Only the
 * members the ported code uses are here; most of the file exists to work
 * around JavaScript array ergonomics that Go's slices and generics already
 * provide.
 */

package common

// AppendArray appends elementsToPush to to, returning the (possibly
// reallocated) slice.
//
// The TypeScript mutates `to` in place and returns nothing, and splits on a
// 256-element threshold because `to.push(...elements)` blows the argument
// limit on large arrays. Go has no such limit, so the split is gone; the
// signature has to return the slice because Go append may reallocate.
func AppendArray[T any](to []T, elementsToPush []T) []T {
	return append(to, elementsToPush...)
}

// Partition works like Array.filter except that it returns a second array with
// the filtered elements.
func Partition[T any](array []T, cb func(value T) bool) (trueItems []T, falseItems []T) {
	trueItems = []T{}
	falseItems = []T{}

	for _, item := range array {
		if cb(item) {
			trueItems = append(trueItems, item)
		} else {
			falseItems = append(falseItems, item)
		}
	}

	return trueItems, falseItems
}
