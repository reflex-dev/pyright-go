/*
 * typeevaluator_subclass.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * createSubclass.
 *
 * The synthesized intersection `<subclass of A and B>`. isinstance narrowing
 * reaches this when the declared type and the filter class have no relation but
 * neither is final: the program is probably using one as a mix-in or a protocol,
 * so rather than narrowing to Never, a class deriving from both is invented.
 *
 * Two details carry weight. The metaclass of the intersection is the narrower of
 * the two, which is why type2's is preferred when it assigns to type1's. And
 * `type[A]` intersected with `type[B]` yields `type[A & B]` rather than
 * `type[A] & type[B]` -- both operands are demoted to instances, intersected,
 * and the result promoted back, which is what createClassObject tracks.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/parser"
)

// CreateSubclass corresponds to createSubclass.
func (e *typeEvaluator) CreateSubclass(
	errorNode parser.ExpressionNode, type1 *ClassType, type2 *ClassType,
) *ClassType {
	// The original asserts both arguments are instantiable classes.

	// The original's comment: if both classes are class objects (type[A] and
	// type[B]), create a new class object (type[A & B]) rather than
	// "type[A] & type[B]".
	createClassObject := false
	if type1.Base().GetInstantiableDepth() > 0 && type2.Base().GetInstantiableDepth() > 0 {
		type1 = ClassTypeCloneAsInstance(type1, true)
		type2 = ClassTypeCloneAsInstance(type2, true)
		createClassObject = true
	}

	printOptions := &PrintTypeOptions{OmitTypeArgsIfUnknown: true}
	className := "<subclass of " +
		e.PrintType(ConvertToInstance(type1, true), printOptions) + " and " +
		e.PrintType(ConvertToInstance(type2, true), printOptions) + ">"
	fileInfo := GetFileInfo(errorNode)

	// The original's comment: the effective metaclass of the intersection is the
	// narrower of the two metaclasses.
	effectiveMetaclass := type1.Shared.EffectiveMetaclass
	if type2.Shared.EffectiveMetaclass != nil {
		if effectiveMetaclass == nil ||
			e.AssignType(effectiveMetaclass, type2.Shared.EffectiveMetaclass,
				nil, nil, AssignTypeFlagsDefault, 0) {
			effectiveMetaclass = type2.Shared.EffectiveMetaclass
		}
	}

	newClassType := ClassTypeCreateInstantiable(
		className,
		GetClassFullName(errorNode, fileInfo.ModuleName, className),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsNone,
		GetTypeSourceID(errorNode),
		nil,
		effectiveMetaclass,
		type1.Shared.DocString,
	)

	newClassType.Shared.BaseClasses = []Type{type1, type2}
	ComputeMroLinearization(newClassType)

	var result Type = newClassType
	result = AddConditionToType(result, propsCondition(type1), nil)
	result = AddConditionToType(result, propsCondition(type2), nil)

	if createClassObject {
		return ClassTypeCloneAsInstantiable(result.(*ClassType), true)
	}

	return result.(*ClassType)
}
