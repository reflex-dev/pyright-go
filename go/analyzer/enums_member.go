/*
 * enums_member.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/enums.ts (pyright 1.1.412):
 * transformTypeForEnumMember, getTypeOfEnumMember, getEnumDeclaredValueType,
 * getEnumAutoValueType, isReprEnumClass.
 *
 * Enum members are not ordinary class attributes. `Color.RED` has type
 * `Literal[Color.RED]`, not whatever was on the right of the `=`, and that
 * literal carries the assigned value along inside it so `Color.RED.value` can be
 * answered later.
 *
 * Deciding WHICH assignments in an enum body are members is most of the work,
 * and the rule is a list of exclusions rather than an inclusion:
 *
 *   - Names with a single leading and trailing underscore (`_name_`, and all
 *     dunders) are reserved by the machinery.
 *   - `name` and `value` are reserved by the metaclass.
 *   - Private (mangled) names are excluded.
 *   - Descriptors are excluded -- a value with `__get__` behaves as a
 *     descriptor, not a member.
 *   - Callables are excluded, which the enum spec does not say explicitly but
 *     which the original notes is the observed behavior.
 *   - An annotated name with no assignment is a declaration, not a member.
 *
 * Then two Python 3.11 features override all of it: `enum.member()` forces
 * something to be a member, and `enum.nonmember()` forces it not to be. And a
 * nested CLASS is a member before Python 3.13 and not after.
 *
 * `name` and `value` are synthesized rather than looked up, and both have a
 * union form for when the receiver is the enum class rather than one member.
 * `value` is the more delicate of the two: a custom metaclass, `__new__` or
 * `__init__` may compute it, so any of those makes the port fall back on the
 * declared type of `_value_` rather than the literal's own.
 *
 * The recursion stack exists because one member may alias another --
 * `B = A` inside an enum makes `B` the same member -- and a cycle among those
 * would otherwise not terminate.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/parser"
)

// enumEvalStackEntry corresponds to EnumEvalStackEntry.
type enumEvalStackEntry struct {
	ClassType  *ClassType
	MemberName string
}

// The module-level enumEvalStack, which breaks cycles among members that alias
// one another, lives on the typeEvaluator here -- see the field's comment. This
// helper reaches it from the interface value every caller passes in;
// *typeEvaluator is the interface's only implementation.
func enumEvalStackOf(evaluator TypeEvaluator) *[]enumEvalStackEntry {
	return &evaluator.(*typeEvaluator).enumEvalStack
}

// TransformTypeForEnumMember corresponds to transformTypeForEnumMember. It
// returns nil where the original returns undefined, meaning "not a member".
func TransformTypeForEnumMember(
	evaluator TypeEvaluator, classType *ClassType, memberName string,
) Type {
	return transformTypeForEnumMember(evaluator, classType, memberName, false, 0)
}

func transformTypeForEnumMember(
	evaluator TypeEvaluator,
	classType *ClassType,
	memberName string,
	ignoreAnnotation bool,
	recursionCount int,
) Type {
	if !ClassTypeIsEnumClass(classType) {
		return nil
	}

	if recursionCount > MaxTypeRecursionCount {
		return nil
	}
	recursionCount++

	// The original's comment: avoid infinite recursion.
	enumEvalStack := enumEvalStackOf(evaluator)
	for _, entry := range *enumEvalStack {
		if ClassTypeIsSameGenericClass(entry.ClassType, classType, 0) && entry.MemberName == memberName {
			return nil
		}
	}

	*enumEvalStack = append(*enumEvalStack, enumEvalStackEntry{ClassType: classType, MemberName: memberName})
	defer func() { *enumEvalStack = (*enumEvalStack)[:len(*enumEvalStack)-1] }()

	memberInfo := LookUpClassMember(classType, memberName, MemberAccessFlagsDefault, nil)
	if memberInfo == nil || !IsClass(memberInfo.ClassType) ||
		!ClassTypeIsEnumClass(memberInfo.ClassType.(*ClassType)) {
		return nil
	}

	decls := memberInfo.Symbol.GetDeclarations()
	if len(decls) < 1 {
		return nil
	}

	primaryDeclNode := decls[0].DeclBase().Node

	shape := readEnumMemberShape(primaryDeclNode, ignoreAnnotation)
	if shape == nil {
		return nil
	}

	// The original's comment: the spec specifically excludes names that start and
	// end with a single underscore. This also includes dunder names.
	if IsSingleDunderName(memberName) {
		return nil
	}

	// The original's comment: specifically exclude "value" and "name". These are
	// reserved by the enum metaclass.
	if memberName == "name" || memberName == "value" {
		return nil
	}

	var declaredType Type
	if shape.DeclaredTypeNode != nil {
		declaredType = evaluator.GetTypeOfAnnotation(shape.DeclaredTypeNode, nil)
	}

	var assignedType Type
	if shape.ValueTypeExprNode != nil {
		evalFlags := EvalFlagsNone
		if GetFileInfo(shape.ValueTypeExprNode).IsStubFile {
			evalFlags = EvalFlagsConvertEllipsisToAny
		}
		assignedType = evaluator.GetTypeOfExpression(shape.ValueTypeExprNode, evalFlags, nil).Type
	}

	// The original's comment: handle aliases to other enum members within the same
	// enum.
	if aliasName, ok := enumAliasTarget(shape.ValueTypeExprNode, memberName); ok {
		aliasedEnumType := transformTypeForEnumMember(evaluator, classType, aliasName, false, recursionCount)

		if aliasClass, isClass := aliasedEnumType.(*ClassType); isClass &&
			IsClassInstance(aliasedEnumType) &&
			ClassTypeIsSameGenericClass(aliasClass,
				ClassTypeCloneAsInstance(memberInfo.ClassType.(*ClassType), true), 0) &&
			aliasClass.Priv.LiteralValue != nil {
			return aliasedEnumType
		}
	}

	assignedType, shape.IsMemberOfEnumeration = enumAssignedTypeForDecl(
		evaluator, primaryDeclNode, assignedType, shape.IsMemberOfEnumeration)

	valueType := declaredType
	if valueType == nil {
		valueType = assignedType
	}
	if valueType == nil {
		valueType = UnknownTypeCreate(false)
	}

	// The original's comment: if the LHS is an unpacked tuple, we need to handle
	// this as a special case.
	if shape.IsUnpackedTuple {
		iterator := evaluator.GetTypeOfIterator(&TypeResult{Type: valueType}, false,
			shape.NameNode, boolPtr(false))
		if iterator != nil {
			valueType = iterator.Type
		} else {
			valueType = UnknownTypeCreate(false)
		}
	}

	if !enumValueTypeIsEligible(valueType, memberName) {
		return nil
	}

	if assignedType == nil && shape.AssignmentRightExpr != nil {
		assignedType = evaluator.GetTypeOfExpression(shape.AssignmentRightExpr, EvalFlagsNone,
			MakeInferenceContext(declaredType, false, nil)).Type
	}

	// The original's comment: handle the Python 3.11 "enum.member()" and
	// "enum.nonmember()" features.
	if assignedClass, ok := assignedType.(*ClassType); ok && IsClassInstance(assignedType) &&
		ClassTypeIsBuiltIn(assignedClass) {
		switch assignedClass.Shared.FullName {
		case "enum.nonmember":
			nonMemberType := firstTypeArgOrUnknown(assignedClass)

			// The original's comment: if the type of the nonmember is declared and
			// the assigned value has a compatible type, use the declared type.
			if declaredType != nil && evaluator.AssignType(declaredType, nonMemberType,
				nil, nil, AssignTypeFlagsDefault, 0) {
				return declaredType
			}

			return nonMemberType

		case "enum.member":
			valueType = firstTypeArgOrUnknown(assignedClass)
			shape.IsMemberOfEnumeration = true
		}
	}

	if !shape.IsMemberOfEnumeration {
		return nil
	}

	memberClass := memberInfo.ClassType.(*ClassType)
	enumLiteral := NewEnumLiteral(memberClass.Shared.FullName, memberClass.Shared.Name,
		memberName, valueType, isReprEnumClass(classType))

	return ClassTypeCloneAsInstance(ClassTypeCloneWithLiteral(memberClass, enumLiteral), true)
}

// enumMemberShape is what the original reads out of the declaration node before
// deciding anything else.
type enumMemberShape struct {
	IsMemberOfEnumeration bool
	IsUnpackedTuple       bool
	ValueTypeExprNode     parser.ExpressionNode
	DeclaredTypeNode      parser.ExpressionNode
	NameNode              *parser.NameNode

	// AssignmentRightExpr is the original's re-read of
	// `nameNode.parent.d.rightExpr` in the late `!assignedType` branch.
	AssignmentRightExpr parser.ExpressionNode
}

// readEnumMemberShape is the original's declaration-node dispatch. It returns
// nil for a declaration shape that cannot be a member at all.
func readEnumMemberShape(primaryDeclNode parser.ParseNode, ignoreAnnotation bool) *enumMemberShape {
	var nameNode *parser.NameNode

	switch typed := primaryDeclNode.(type) {
	case *parser.NameNode:
		nameNode = typed
	case *parser.FunctionNode:
		// The original's comment: handle the case where a method or class is
		// decorated with @enum.member.
		nameNode = typed.D.Name
	case *parser.ClassNode:
		nameNode = typed.D.Name
	default:
		return nil
	}

	shape := &enumMemberShape{NameNode: nameNode}
	parent := nameNode.NodeBase().Parent

	switch parentTyped := parent.(type) {
	case *parser.AssignmentNode:
		if parentTyped.D.LeftExpr == parser.ExpressionNode(nameNode) {
			shape.IsMemberOfEnumeration = true
			shape.ValueTypeExprNode = parentTyped.D.RightExpr
			shape.AssignmentRightExpr = parentTyped.D.RightExpr
		}

	case *parser.TupleNode:
		if grandparent, ok := parentTyped.NodeBase().Parent.(*parser.AssignmentNode); ok {
			shape.IsMemberOfEnumeration = true
			shape.IsUnpackedTuple = true
			shape.ValueTypeExprNode = grandparent.D.RightExpr
		}

	case *parser.TypeAnnotationNode:
		if parentTyped.D.ValueExpr == parser.ExpressionNode(nameNode) {
			if ignoreAnnotation {
				shape.IsMemberOfEnumeration = true
			}
			shape.DeclaredTypeNode = parentTyped.D.Annotation
		}
	}

	return shape
}

// enumAliasTarget is the original's `valueTypeExprNode?.nodeType === Name &&
// value !== memberName` test.
func enumAliasTarget(valueTypeExprNode parser.ExpressionNode, memberName string) (string, bool) {
	nameNode, ok := valueTypeExprNode.(*parser.NameNode)
	if !ok || nameNode.D.Value == memberName {
		return "", false
	}
	return nameNode.D.Value, true
}

// enumAssignedTypeForDecl is the original's function and class arms: a decorated
// method or a nested class takes its assigned type from the decorated result,
// and a nested class is a member only before Python 3.13.
func enumAssignedTypeForDecl(
	evaluator TypeEvaluator,
	primaryDeclNode parser.ParseNode,
	assignedType Type,
	isMemberOfEnumeration bool,
) (Type, bool) {
	switch typed := primaryDeclNode.(type) {
	case *parser.FunctionNode:
		if functionTypeInfo := evaluator.GetTypeOfFunction(typed); functionTypeInfo != nil {
			assignedType = functionTypeInfo.DecoratedType
		}

	case *parser.ClassNode:
		classTypeInfo := evaluator.GetTypeOfClass(typed)
		if classTypeInfo == nil {
			break
		}
		assignedType = classTypeInfo.DecoratedType

		// The original's comment: if the class is not marked as a member or a
		// non-member, the behavior depends on the version of Python. In versions
		// prior to 3.13, classes are treated as members.
		if IsInstantiableClass(assignedType) {
			fileInfo := GetFileInfo(typed)
			isMemberOfEnumeration = fileInfo.ExecutionEnvironment.PythonVersion.IsLessThan(
				common.PythonVersion3_13)
		}
	}

	return assignedType, isMemberOfEnumeration
}

// enumValueTypeIsEligible is the original's three exclusions on the value type.
func enumValueTypeIsEligible(valueType Type, memberName string) bool {
	// The original's comment: the spec excludes descriptors.
	if valueClass, ok := valueType.(*ClassType); ok && IsClassInstance(valueType) {
		if _, found := ClassTypeGetSymbolTable(valueClass).Get("__get__"); found {
			return false
		}
	}

	// The original's comment: the spec excludes private (mangled) names.
	if IsPrivateName(memberName) {
		return false
	}

	// The original's comment: the enum spec doesn't explicitly specify this, but
	// it appears that callables are excluded.
	return FindSubtype(valueType, func(subtype Type) bool {
		return !IsFunctionOrOverloaded(subtype)
	}) != nil
}

// firstTypeArgOrUnknown is the original's
// `typeArgs && typeArgs.length > 0 ? typeArgs[0] : UnknownType.create()`.
func firstTypeArgOrUnknown(classType *ClassType) Type {
	if len(classType.Priv.TypeArgs) > 0 {
		return classType.Priv.TypeArgs[0]
	}
	return UnknownTypeCreate(false)
}

// GetEnumDeclaredValueType corresponds to getEnumDeclaredValueType.
func GetEnumDeclaredValueType(
	evaluator TypeEvaluator, classType *ClassType, declaredTypesOnly bool,
) Type {
	flags := MemberAccessFlagsDefault
	if declaredTypesOnly {
		flags = MemberAccessFlagsDeclaredTypesOnly
	}

	declaredValueMember := LookUpClassMember(classType, "_value_", flags, nil)

	// The original's comment: if the declared type comes from the 'Enum' base
	// class, ignore it because it will be "Any", which isn't useful to us here.
	if declaredValueMember != nil && declaredValueMember.ClassType != nil &&
		IsClass(declaredValueMember.ClassType) &&
		!ClassTypeIsBuiltInNamed(declaredValueMember.ClassType.(*ClassType), "Enum") {
		return evaluator.GetTypeOfMember(declaredValueMember)
	}

	return nil
}

// GetTypeOfEnumMember corresponds to getTypeOfEnumMember.
func GetTypeOfEnumMember(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	memberName string,
	isIncomplete bool,
) *TypeResult {
	if !ClassTypeIsEnumClass(classType) {
		return nil
	}

	if t := TransformTypeForEnumMember(evaluator, classType, memberName); t != nil {
		return &TypeResult{Type: t, IsIncomplete: isIncomplete}
	}

	if classType.Base().IsInstantiable() {
		return nil
	}

	// The original's comment: handle the special case of 'name' and 'value'
	// members within an enum.
	literalValue := classType.Priv.LiteralValue

	if memberName == "name" || memberName == "_name_" {
		if result, handled := enumNameMember(evaluator, errorNode, classType,
			memberName, literalValue, isIncomplete); handled {
			return result
		}
	}

	// The original's comment: see if there is a declared type for "_value_".
	valueType := GetEnumDeclaredValueType(evaluator, classType, false)

	if memberName == "value" || memberName == "_value_" {
		return enumValueMember(evaluator, classType, memberName, literalValue, valueType, isIncomplete)
	}

	return nil
}

// enumNameMember is the original's `name`/`_name_` block. The second return
// reports whether it produced the answer.
func enumNameMember(
	evaluator TypeEvaluator,
	errorNode parser.ParseNode,
	classType *ClassType,
	memberName string,
	literalValue LiteralValue,
	isIncomplete bool,
) (*TypeResult, bool) {
	// The original's comment: does the class explicitly override this member? Or
	// it it using the standard behavior provided by the "Enum" class?
	memberInfo := LookUpClassMember(classType, memberName, MemberAccessFlagsDefault, nil)
	if memberInfo != nil && IsClass(memberInfo.ClassType) &&
		!ClassTypeIsBuiltInNamed(memberInfo.ClassType.(*ClassType), "Enum") {
		return nil, true
	}

	strClass := evaluator.GetBuiltInType(errorNode, "str")
	if !IsInstantiableClass(strClass) {
		return nil, true
	}

	makeNameType := func(value *EnumLiteral) Type {
		return ClassTypeCloneAsInstance(
			ClassTypeCloneWithLiteral(strClass.(*ClassType), LiteralString(value.ItemName)), true)
	}

	if literalValue != nil {
		enumLiteral, ok := literalValue.(*EnumLiteral)
		assert(ok, "expected an EnumLiteral")
		return &TypeResult{Type: makeNameType(enumLiteral), IsIncomplete: isIncomplete}, true
	}

	// The original's comment: the type wasn't associated with a particular enum
	// literal, so return a union of all possible enum literals.
	literalValues := EnumerateLiteralsForType(evaluator, classType)
	if len(literalValues) == 0 {
		return nil, false
	}

	names := make([]Type, len(literalValues))
	for i, literalClass := range literalValues {
		enumLiteral, ok := literalClass.Priv.LiteralValue.(*EnumLiteral)
		assert(ok, "expected an EnumLiteral")
		names[i] = makeNameType(enumLiteral)
	}

	return &TypeResult{Type: CombineTypes(names, nil), IsIncomplete: isIncomplete}, true
}

// enumValueMember is the original's `value`/`_value_` block.
func enumValueMember(
	evaluator TypeEvaluator,
	classType *ClassType,
	memberName string,
	literalValue LiteralValue,
	valueType Type,
	isIncomplete bool,
) *TypeResult {
	// The original's comment: does the class explicitly override this member? Or
	// it it using the standard behavior provided by the "Enum" class and other
	// built-in subclasses like "StrEnum" and "IntEnum"?
	memberInfo := LookUpClassMember(classType, memberName, MemberAccessFlagsDefault, nil)
	if memberInfo != nil && IsClass(memberInfo.ClassType) &&
		!ClassTypeIsBuiltIn(memberInfo.ClassType.(*ClassType)) {
		return nil
	}

	valueOrAny := func() *TypeResult {
		if valueType != nil {
			return &TypeResult{Type: valueType, IsIncomplete: isIncomplete}
		}
		return &TypeResult{Type: AnyTypeCreate(false), IsIncomplete: isIncomplete}
	}

	// The original's comment: if the enum class has a custom metaclass, it may
	// implement some "magic" that computes different values for the "_value_"
	// attribute. This occurs, for example, in the django TextChoices class. If we
	// detect a custom metaclass, we'll use the declared type of _value_ if it is
	// declared.
	metaclass := classType.Shared.EffectiveMetaclass
	if metaclass != nil && IsClass(metaclass) && !ClassTypeIsBuiltIn(metaclass.(*ClassType)) {
		return valueOrAny()
	}

	// The original's comment: if the enum class has a custom __new__ or __init__
	// method, it may implement some magic that computes different values for the
	// "_value_" attribute. If we see a customer __new__ or __init__, we'll assume
	// the value type is what we computed above, or Any.
	newMember := LookUpClassMember(classType, "__new__", MemberAccessFlagsSkipObjectBaseClass, nil)
	initMember := LookUpClassMember(classType, "__init__", MemberAccessFlagsSkipObjectBaseClass, nil)

	if newMember != nil && IsClass(newMember.ClassType) &&
		!ClassTypeIsBuiltIn(newMember.ClassType.(*ClassType)) {
		return valueOrAny()
	}

	if initMember != nil && IsClass(initMember.ClassType) &&
		!ClassTypeIsBuiltIn(initMember.ClassType.(*ClassType)) {
		return valueOrAny()
	}

	// The original's comment: there were no explicit assignments to the "_value_"
	// attribute, so we can assume that the values are assigned directly to the
	// "_value_" by the EnumMeta metaclass.
	if literalValue != nil {
		enumLiteral, ok := literalValue.(*EnumLiteral)
		assert(ok, "expected an EnumLiteral")

		// The original's comment: if there is no known value type for this literal
		// value, return undefined. This will cause the caller to fall back on the
		// definition of "_value_" within the class definition (if present).
		if IsAny(enumLiteral.ItemType) {
			if valueType != nil {
				return &TypeResult{Type: valueType, IsIncomplete: isIncomplete}
			}
			return nil
		}

		return &TypeResult{Type: enumLiteral.ItemType, IsIncomplete: isIncomplete}
	}

	// The original's comment: the type wasn't associated with a particular enum
	// literal, so return a union of all possible enum literals.
	literalValues := EnumerateLiteralsForType(evaluator, classType)
	if len(literalValues) == 0 {
		return nil
	}

	itemTypes := make([]Type, len(literalValues))
	for i, literalClass := range literalValues {
		enumLiteral, ok := literalClass.Priv.LiteralValue.(*EnumLiteral)
		assert(ok, "expected an EnumLiteral")
		itemTypes[i] = enumLiteral.ItemType
	}

	return &TypeResult{Type: CombineTypes(itemTypes, nil), IsIncomplete: isIncomplete}
}

// GetEnumAutoValueType corresponds to getEnumAutoValueType: the type
// `enum.auto()` produces, which a `_generate_next_value_` override can change.
func GetEnumAutoValueType(evaluator TypeEvaluator, node parser.ExpressionNode) Type {
	containingClassNode := GetEnclosingClass(node, false)

	if containingClassNode != nil {
		if classTypeInfo := evaluator.GetTypeOfClass(containingClassNode); classTypeInfo != nil {
			memberInfo := evaluator.GetTypeOfBoundMember(node,
				ClassTypeCloneAsInstance(classTypeInfo.ClassType, true),
				"_generate_next_value_", nil, nil, MemberAccessFlagsDefault, nil)

			// The original's comment: did we find a custom _generate_next_value_
			// sunder override? Ignore if this comes from Enum because it is declared
			// as returning an "Any" type in the typeshed stubs.
			if memberInfo != nil && !memberInfo.TypeErrors && IsFunction(memberInfo.Type) &&
				memberInfo.ClassType != nil && IsClass(memberInfo.ClassType) &&
				!ClassTypeIsBuiltInNamed(memberInfo.ClassType.(*ClassType), "Enum") {
				if declared := memberInfo.Type.(*FunctionType).Shared.DeclaredReturnType; declared != nil {
					return declared
				}
			}
		}
	}

	return evaluator.GetBuiltInObject(node, "int", nil)
}

// isReprEnumClass corresponds to the function of the same name.
func isReprEnumClass(enumClass *ClassType) bool {
	for _, mroClass := range enumClass.Shared.Mro {
		if cls, ok := mroClass.(*ClassType); ok && IsClass(mroClass) &&
			ClassTypeIsBuiltInNamed(cls, "ReprEnum") {
			return true
		}
	}
	return false
}
