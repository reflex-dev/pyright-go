/*
 * enums.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/enums.ts (pyright 1.1.412).
 *
 * Only isEnumMetaclass so far; class creation consults it to decide whether a
 * class carries the enum flag. The rest of the module arrives with enum member
 * handling.
 */

package analyzer

// IsEnumMetaclass corresponds to isEnumMetaclass.
func IsEnumMetaclass(classType *ClassType) bool {
	for _, mroClass := range classType.Shared.Mro {
		if IsClass(mroClass) && ClassTypeIsBuiltInNamed(mroClass.(*ClassType), "EnumMeta", "EnumType") {
			return true
		}
	}
	return false
}
