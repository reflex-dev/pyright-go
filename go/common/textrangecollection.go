/*
 * textrangecollection.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * Based on code from vscode-python repository:
 *  https://github.com/Microsoft/vscode-python
 *
 * Class that maintains an ordered list of text ranges and allows
 * for indexing and fast lookups within this list.
 *
 * Transliterated from common/textRangeCollection.ts (pyright 1.1.412).
 */

package common

// RangeItem is the Go stand-in for the TypeScript `T extends TextRange`
// constraint.
type RangeItem interface {
	GetRange() TextRange
}

// TextRangeCollection maintains an ordered list of text ranges.
type TextRangeCollection[T RangeItem] struct {
	items []T
}

// NewTextRangeCollection constructs a collection over items.
func NewTextRangeCollection[T RangeItem](items []T) *TextRangeCollection[T] {
	return &TextRangeCollection[T]{items: items}
}

// Items exposes the underlying slice.
func (c *TextRangeCollection[T]) Items() []T {
	return c.items
}

// Start corresponds to the `start` accessor.
func (c *TextRangeCollection[T]) Start() int {
	if len(c.items) > 0 {
		return c.items[0].GetRange().Start
	}
	return 0
}

// End corresponds to the `end` accessor.
func (c *TextRangeCollection[T]) End() int {
	if len(c.items) > 0 {
		lastItem := c.items[len(c.items)-1].GetRange()
		return lastItem.Start + lastItem.Length
	}
	return 0
}

// Length corresponds to the `length` accessor.
func (c *TextRangeCollection[T]) Length() int {
	return c.End() - c.Start()
}

// Count corresponds to the `count` accessor.
func (c *TextRangeCollection[T]) Count() int {
	return len(c.items)
}

// Contains corresponds to contains().
func (c *TextRangeCollection[T]) Contains(position int) bool {
	return position >= c.Start() && position < c.End()
}

// GetItemAt corresponds to getItemAt().
func (c *TextRangeCollection[T]) GetItemAt(index int) T {
	if index < 0 || index >= len(c.items) {
		Fail("index is out of range")
	}
	return c.items[index]
}

// GetItemAtPosition returns the nearest item prior to the position. The
// position may not be contained within the item.
func (c *TextRangeCollection[T]) GetItemAtPosition(position int) int {
	if c.Count() == 0 {
		return -1
	}
	if position < c.Start() {
		return -1
	}
	if position > c.End() {
		return -1
	}

	min := 0
	max := c.Count() - 1

	for min < max {
		mid := min + ((max - min) >> 1)
		item := c.items[mid].GetRange()

		// Is the position past the start of this item but before
		// the start of the next item? If so, we found our item.
		if position >= item.Start {
			if mid >= c.Count()-1 || position < c.items[mid+1].GetRange().Start {
				return mid
			}
		}

		if position < item.Start {
			max = mid - 1
		} else {
			min = mid + 1
		}
	}
	return min
}

// GetItemContaining corresponds to getItemContaining().
func (c *TextRangeCollection[T]) GetItemContaining(position int) int {
	if c.Count() == 0 {
		return -1
	}
	if position < c.Start() {
		return -1
	}
	if position > c.End() {
		return -1
	}

	return GetIndexContaining(c.items, position, nil)
}

// GetIndexContaining corresponds to getIndexContaining(). Pass nil for inRange
// to get the default TextRange.contains behavior.
//
// The TypeScript signature accepts `(T | undefined)[]`, i.e. an array that may
// have holes, and the helper searches outward from the probe index to find a
// present element. Every call site in the ported subset passes a dense slice,
// so "absent" here means only "index out of bounds" -- which is exactly the
// case the TypeScript version also hits when probing mid+1 past the end, and
// which makes it return -1. That path is preserved.
func GetIndexContaining[T RangeItem](arr []T, position int, inRange func(item T, position int) bool) int {
	if inRange == nil {
		inRange = func(item T, position int) bool {
			return item.GetRange().Contains(position)
		}
	}

	if len(arr) == 0 {
		return -1
	}

	min := 0
	max := len(arr) - 1
	for min <= max {
		mid := min + (max-min)/2
		elementIndex, element, ok := findPresentElement(arr, mid, min, max)
		if !ok {
			return -1
		}

		if inRange(element, position) {
			return elementIndex
		}

		_, nextElement, ok := findPresentElement(arr, mid+1, mid+1, max)
		if !ok {
			return -1
		}

		if mid < len(arr)-1 && element.GetRange().End() <= position && position < nextElement.GetRange().Start {
			return -1
		}

		if position < element.GetRange().Start {
			max = mid - 1
		} else {
			min = mid + 1
		}
	}

	return -1
}

// findPresentElement corresponds to findNonNullElement().
func findPresentElement[T RangeItem](arr []T, position, min, max int) (int, T, bool) {
	if position >= 0 && position < len(arr) {
		return position, arr[position], true
	}

	// Search forward and backward until it finds a present value.
	for i := position + 1; i <= max; i++ {
		if i >= 0 && i < len(arr) {
			return i, arr[i], true
		}
	}

	for i := position - 1; i >= min; i-- {
		if i >= 0 && i < len(arr) {
			return i, arr[i], true
		}
	}

	var zero T
	return -1, zero, false
}
