/*
 * typeevaluator_indexbase.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfIndexWithBaseType and getIndexAccessMagicMethodName.
 *
 * This is where an index expression finally becomes one thing or the other. Four
 * special cases are tried in order before the general path -- a generic type
 * alias being specialized, a Never or NoReturn special form, a TypeAliasType
 * being specialized in a value expression, and a recursive type alias
 * placeholder -- and only then does it map over the base type's subtypes.
 *
 * The subtype mapping is the specialization-versus-subscript decision. An
 * instantiable class means `list[int]`; a class instance means `x[0]` and goes
 * to __getitem__/__setitem__/__delitem__ by usage. Within the instantiable arm
 * there are seven more special cases (Literal, InitVar, Enum, Annotated, custom
 * __class_getitem__, Final, ClassVar) before ordinary specialization.
 *
 * It was the largest remaining frontier entry at 1,340 hits.
 */

package analyzer

import (
	"math/big"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfIndexWithBaseType corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfIndexWithBaseType(
	node *parser.IndexNode,
	baseTypeResult *TypeResult,
	usage *EvaluatorUsage,
	flags EvalFlags,
) *TypeResult {
	// The original's comment: handle the case where we're specializing a generic
	// type alias.
	if typeAliasResult := e.createSpecializedTypeAlias(node, baseTypeResult.Type, flags); typeAliasResult != nil {
		return &typeAliasResult.TypeResult
	}

	// The original's comment: handle the case where Never or NoReturn are being
	// specialized. It swaps in the special form type, which is the Never or
	// NoReturn class -- and copies rather than mutating, since baseTypeResult may
	// be the cached one.
	if IsNever(baseTypeResult.Type) {
		if props := baseTypeResult.Type.Base().Props; props != nil && props.SpecialForm != nil {
			copied := *baseTypeResult
			copied.Type = props.SpecialForm
			baseTypeResult = &copied
		}
	}

	// The original's comment: handle the case where a TypeAliasType symbol is
	// being specialized in a value expression.
	if result := e.specializeTypeAliasTypeInValueExpr(node, baseTypeResult, flags); result != nil {
		return result
	}

	if IsTypeVar(baseTypeResult.Type) && IsTypeAliasPlaceholder(baseTypeResult.Type) {
		typeArgTypes := []Type{}
		for _, arg := range e.getTypeArgs(node, flags, nil) {
			typeArgTypes = append(typeArgTypes, ConvertToInstance(arg.Type, true))
		}
		return &TypeResult{Type: CloneForTypeAlias(baseTypeResult.Type, &TypeAliasInfo{
			Shared:   baseTypeResult.Type.(*TypeVarType).Shared.RecursiveAlias,
			TypeArgs: typeArgTypes,
		})}
	}

	state := &indexBaseState{isIncomplete: baseTypeResult.IsIncomplete}

	t := e.MapSubtypesExpandTypeVars(baseTypeResult.Type, nil, func(concreteSubtype Type, unexpandedSubtype Type) Type {
		return e.indexOneSubtype(node, usage, flags, concreteSubtype, unexpandedSubtype, state)
	})

	// The original's comment: in case we didn't walk the list items above, do so
	// now. If we have, this information will be cached.
	if !baseTypeResult.IsIncomplete {
		for _, item := range node.D.Items {
			if !e.isTypeCached(item.D.ValueExpr) {
				e.getTypeOfExpression(item.D.ValueExpr, flags&EvalFlagsForwardRefs, nil)
			}
		}
	}

	return &TypeResult{
		Type:          t,
		IsIncomplete:  state.isIncomplete,
		IsReadOnly:    state.isReadOnly,
		IsRequired:    state.isRequired,
		IsNotRequired: state.isNotRequired,
	}
}

// indexBaseState carries the four bindings the original's mapSubtypes callback
// closes over and mutates.
type indexBaseState struct {
	isIncomplete  bool
	isRequired    bool
	isNotRequired bool
	isReadOnly    bool
}

// specializeTypeAliasTypeInValueExpr is the original's TypeAliasType block.
func (e *typeEvaluator) specializeTypeAliasTypeInValueExpr(
	node *parser.IndexNode,
	baseTypeResult *TypeResult,
	flags EvalFlags,
) *TypeResult {
	if !IsClassInstance(baseTypeResult.Type) ||
		!ClassTypeIsBuiltInNamed(baseTypeResult.Type.(*ClassType), "TypeAliasType") {
		return nil
	}

	props := baseTypeResult.Type.Base().Props
	if props == nil || props.TypeForm == nil {
		return nil
	}

	typeFormProps := props.TypeForm.Base().Props
	if typeFormProps == nil || typeFormProps.TypeAliasInfo == nil ||
		typeFormProps.TypeAliasInfo.Shared == nil || typeFormProps.TypeAliasInfo.Shared.TypeParams == nil {
		return nil
	}

	// `{ ...typeAliasInfo, typeArgs: undefined }` -- the shared info without the
	// arguments, so createSpecializedTypeAlias sees an unspecialized alias.
	origTypeAlias := CloneForTypeAlias(
		ConvertToInstantiable(props.TypeForm, true),
		&TypeAliasInfo{Shared: typeFormProps.TypeAliasInfo.Shared},
	)

	typeFormType := e.createSpecializedTypeAlias(node, origTypeAlias, flags)
	if typeFormType == nil {
		return nil
	}

	return &TypeResult{
		Type: CloneWithTypeForm(baseTypeResult.Type, ConvertToInstance(typeFormType.Type, true)),
	}
}

// indexOneSubtype is one iteration of the original's mapSubtypesExpandTypeVars
// callback.
func (e *typeEvaluator) indexOneSubtype(
	node *parser.IndexNode,
	usage *EvaluatorUsage,
	flags EvalFlags,
	concreteSubtype Type,
	unexpandedSubtype Type,
	state *indexBaseState,
) Type {
	var selfType Type
	if IsTypeVar(unexpandedSubtype) {
		selfType = unexpandedSubtype
	}

	if IsAnyOrUnknown(concreteSubtype) {
		if (flags & EvalFlagsTypeExpression) != 0 {
			// The original's comment: if we are expecting a type annotation
			// here, assume that the subscripts are type arguments and evaluate
			// them accordingly.
			e.getTypeArgs(node, flags, nil)
		}

		return concreteSubtype
	}

	if (flags & EvalFlagsInstantiableType) != 0 {
		if IsTypeVar(unexpandedSubtype) {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarNotSubscriptable().Format(e.PrintType(unexpandedSubtype, nil)),
				node.D.LeftExpr,
				nil,
			)

			// The original's comment: evaluate the index expressions as though
			// they are type arguments for error-reporting.
			e.getTypeArgs(node, flags, nil)

			return UnknownTypeCreate(false)
		}
	}

	if IsInstantiableClass(concreteSubtype) {
		return e.indexInstantiableClass(node, usage, flags, concreteSubtype.(*ClassType), selfType, state)
	}

	if IsNoneInstance(concreteSubtype) {
		if !state.isIncomplete {
			e.AddDiagnostic(
				DiagnosticRuleReportOptionalSubscript,
				localization.LocMessage.NoneNotSubscriptable(),
				node.D.LeftExpr,
				nil,
			)
		}

		return UnknownTypeCreate(false)
	}

	if IsClassInstance(concreteSubtype) {
		typeResult := e.getTypeOfIndexedObjectOrClass(node, concreteSubtype.(*ClassType), selfType, usage)
		if typeResult.IsIncomplete {
			state.isIncomplete = true
		}
		return typeResult.Type
	}

	if IsNever(concreteSubtype) {
		return NeverTypeCreateNever()
	}

	if IsUnbound(concreteSubtype) {
		return UnknownTypeCreate(false)
	}

	if !state.isIncomplete {
		e.AddDiagnostic(
			DiagnosticRuleReportIndexIssue,
			localization.LocMessage.TypeNotSubscriptable().Format(e.PrintType(concreteSubtype, nil)),
			node.D.LeftExpr,
			nil,
		)
	}

	return UnknownTypeCreate(false)
}

// indexInstantiableClass is the original's `if (isInstantiableClass(concreteSubtype))`
// arm: the specialization side of the fork.
func (e *typeEvaluator) indexInstantiableClass(
	node *parser.IndexNode,
	usage *EvaluatorUsage,
	flags EvalFlags,
	concreteSubtype *ClassType,
	selfType Type,
	state *indexBaseState,
) Type {
	// The original's comment: see if the class has a custom metaclass that
	// supports __getitem__, etc.
	if meta := concreteSubtype.Shared.EffectiveMetaclass; meta != nil && IsInstantiableClass(meta) &&
		!ClassTypeIsBuiltInNamed(meta.(*ClassType), "type", "_InitVarMeta") &&
		(flags&EvalFlagsInstantiableType) == 0 {
		itemMethodType := e.GetBoundMagicMethod(
			concreteSubtype, getIndexAccessMagicMethodName(usage), nil, node.D.LeftExpr, nil, 0)

		if (flags & EvalFlagsTypeExpression) != 0 {
			// The original's comment: if the class doesn't derive from Generic,
			// a type argument should not be allowed.
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeArguments,
				localization.LocMessage.TypeArgsExpectingNone().Format(
					e.PrintType(ClassTypeCloneAsInstance(concreteSubtype, true), nil),
				),
				node,
				nil,
			)
		}

		// A metaclass __getitem__ means the subscript is a runtime call, not a
		// specialization, so the result comes from that method rather than from
		// the type arguments.
		if itemMethodType != nil {
			return e.getTypeOfIndexedObjectOrClass(node, concreteSubtype, selfType, usage).Type
		}
	}

	// The original's comment: setting the value of an indexed class will always
	// result in an exception.
	switch usage.Method {
	case "set":
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.GenericClassAssigned(), node.D.LeftExpr, nil)
	case "del":
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.GenericClassDeleted(), node.D.LeftExpr, nil)
	}

	if ClassTypeIsSpecialBuiltInNamed(concreteSubtype, "Literal") {
		// The original's comment: special-case Literal types.
		return e.createLiteralType(concreteSubtype, node, flags)
	}

	if ClassTypeIsBuiltInNamed(concreteSubtype, "InitVar") {
		return e.indexInitVar(node, flags)
	}

	if ClassTypeIsEnumClass(concreteSubtype) {
		// The original's comment: special-case Enum types. It carries two TODOs
		// here about validating the index entry.
		return ClassTypeCloneAsInstance(concreteSubtype, true)
	}

	isAnnotatedClass := ClassTypeIsBuiltInNamed(concreteSubtype, "Annotated")
	hasCustomClassGetItem := ClassTypeHasCustomClassGetItem(concreteSubtype)
	isGenericClass := len(concreteSubtype.Shared.TypeParams) > 0 ||
		ClassTypeIsSpecialBuiltIn(concreteSubtype) ||
		ClassTypeIsBuiltInNamed(concreteSubtype, "type") ||
		ClassTypeIsPartiallyEvaluated(concreteSubtype)
	isFinalAnnotation := ClassTypeIsBuiltInNamed(concreteSubtype, "Final")
	isClassVarAnnotation := ClassTypeIsBuiltInNamed(concreteSubtype, "ClassVar")

	// The original's comment: this feature is currently experimental.
	supportsTypedDictTypeArg := GetFileInfo(node).DiagnosticRuleSet.EnableExperimentalFeatures &&
		ClassTypeIsBuiltInNamed(concreteSubtype, "TypedDict")

	typeArgs := e.getTypeArgs(node, flags, &getTypeArgsOptions{
		IsAnnotatedClass:         isAnnotatedClass,
		HasCustomClassGetItem:    hasCustomClassGetItem || !isGenericClass,
		IsFinalAnnotation:        isFinalAnnotation,
		IsClassVarAnnotation:     isClassVarAnnotation,
		SupportsTypedDictTypeArg: supportsTypedDictTypeArg,
	})

	if !isAnnotatedClass {
		typeArgs = e.adjustTypeArgsForTypeVarTuple(typeArgs, concreteSubtype.Shared.TypeParams, node)
	}

	// The original's comment: if this is a custom __class_getitem__, there's no
	// need to specialize the class. Just return it as is.
	if hasCustomClassGetItem {
		return concreteSubtype
	}

	if concreteSubtype.Priv.TypeArgs != nil {
		e.AddDiagnostic(
			DiagnosticRuleReportInvalidTypeArguments,
			localization.LocMessage.ClassAlreadySpecialized().Format(
				e.PrintType(ConvertToInstance(concreteSubtype, true), &PrintTypeOptions{ExpandTypeAlias: true}),
			),
			node.D.LeftExpr,
			nil,
		)
		return concreteSubtype
	}

	result := e.createSpecializedClassType(concreteSubtype, typeArgs, flags, node)
	if result == nil {
		return UnknownTypeCreate(false)
	}

	if result.IsRequired {
		state.isRequired = true
	} else if result.IsNotRequired {
		state.isNotRequired = true
	}

	if result.IsReadOnly {
		state.isReadOnly = true
	}

	return result.Type
}

// indexInitVar is the original's InitVar special case, lifted out because it is
// the only arm with more than one exit.
func (e *typeEvaluator) indexInitVar(node *parser.IndexNode, flags EvalFlags) Type {
	// The original's comment: special-case InitVar, used in dataclasses.
	typeArgs := e.getTypeArgs(node, flags, nil)
	isTypeFormArg := (flags & EvalFlagsTypeFormArg) != 0

	if (flags&EvalFlagsTypeExpression) != 0 || isTypeFormArg {
		if isTypeFormArg || (flags&EvalFlagsVarTypeAnnotation) == 0 {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.InitVarNotAllowed(),
				node.D.LeftExpr,
				nil,
			)
		}
	}

	if isTypeFormArg {
		return UnknownTypeCreate(false)
	}

	if len(typeArgs) == 1 {
		return typeArgs[0].Type
	}

	e.AddDiagnostic(
		DiagnosticRuleReportInvalidTypeForm,
		localization.LocMessage.TypeArgsMismatchOne().Format(len(typeArgs)),
		node.D.LeftExpr,
		nil,
	)

	return UnknownTypeCreate(false)
}

// getIndexAccessMagicMethodName corresponds to the function of the same name.
func getIndexAccessMagicMethodName(usage *EvaluatorUsage) string {
	switch usage.Method {
	case "get":
		return "__getitem__"
	case "set":
		return "__setitem__"
	default:
		// The original asserts the method is 'del' here.
		return "__delitem__"
	}
}

/*
 * The five things index evaluation reaches that are separate units of work.
 */

// getTypeArgsOptions corresponds to the inline options object getTypeArgs takes.
type getTypeArgsOptions struct {
	IsAnnotatedClass         bool
	HasCustomClassGetItem    bool
	IsFinalAnnotation        bool
	IsClassVarAnnotation     bool
	SupportsTypedDictTypeArg bool
}

// createLiteralType corresponds to the function of the same name: `Literal[...]`.
//
// The original's comment: as per the specification, we support None, int, bool,
// str, bytes literals plus enum values.
//
// Each argument is read SYNTACTICALLY rather than evaluated, because the literal
// value is the point -- `Literal[1]` needs the token `1`, not `int`. Only when
// no syntactic form matches does it fall back on evaluating the expression,
// which catches enum members and aliases to existing literal types.
//
// Outside a type expression an unrecognized argument is not an error; the whole
// thing is just the `Literal` class object. Inside one it is reported.
func (e *typeEvaluator) createLiteralType(
	classType *ClassType, node *parser.IndexNode, flags EvalFlags,
) Type {
	if len(node.D.Items) == 0 {
		e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
			localization.LocMessage.LiteralEmptyArgs(), node.D.LeftExpr, nil)
		return UnknownTypeCreate(false)
	}

	literalTypes := []Type{}
	isValidTypeForm := true

	for _, item := range node.D.Items {
		t := e.literalTypeForItem(node, classType, item, flags, &isValidTypeForm)

		if t == nil {
			t = e.literalTypeFromExpression(item.D.ValueExpr, flags)
		}

		if t == nil {
			if (flags & EvalFlagsTypeExpression) == 0 {
				// Outside a type expression this is just the `Literal` object.
				return ClassTypeCloneAsInstance(classType, true)
			}
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.LiteralUnsupportedType(), item, nil)
			t = UnknownTypeCreate(false)
			isValidTypeForm = false
		}

		literalTypes = append(literalTypes, t)
	}

	result := CombineTypes(literalTypes, &CombineTypesOptions{SkipElideRedundantLiterals: true})

	if IsUnion(result) && e.prefetched != nil && e.prefetched.UnionTypeClass != nil &&
		IsInstantiableClass(e.prefetched.UnionTypeClass) {
		result = CloneAsSpecialForm(result,
			ClassTypeCloneAsInstance(e.prefetched.UnionTypeClass.(*ClassType), true))
	}

	if isValidTypeForm {
		result = CloneWithTypeForm(result, ConvertToInstance(result, true))
	}

	return result
}

// literalTypeForItem is the original's syntactic dispatch over one argument. It
// returns nil when no syntactic form matched.
func (e *typeEvaluator) literalTypeForItem(
	node *parser.IndexNode,
	classType *ClassType,
	item *parser.ArgumentNode,
	flags EvalFlags,
	isValidTypeForm *bool,
) Type {
	itemExpr := item.D.ValueExpr

	if item.D.ArgCategory != parser.ArgCategorySimple {
		if (flags & EvalFlagsTypeExpression) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.UnpackedArgInTypeArgument(), itemExpr, nil)
			*isValidTypeForm = false
			return UnknownTypeCreate(false)
		}
		return nil
	}

	if item.D.Name != nil {
		if (flags & EvalFlagsTypeExpression) != 0 {
			e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.KeywordArgInTypeArgument(), itemExpr, nil)
			*isValidTypeForm = false
			return UnknownTypeCreate(false)
		}
		return nil
	}

	switch typed := itemExpr.(type) {
	case *parser.StringListNode:
		return e.literalTypeForStringList(node, classType, typed, flags, isValidTypeForm)

	case *parser.NumberNode:
		if !typed.D.IsImaginary && typed.D.IsInteger {
			return e.cloneBuiltinClassWithLiteral(node, classType, "int",
				numberNodeLiteralValue(typed.D.Value))
		}

	case *parser.ConstantNode:
		switch typed.D.ConstType {
		case parser.KeywordTypeTrue:
			return e.cloneBuiltinClassWithLiteral(node, classType, "bool", LiteralBool(true))
		case parser.KeywordTypeFalse:
			return e.cloneBuiltinClassWithLiteral(node, classType, "bool", LiteralBool(false))
		case parser.KeywordTypeNone:
			if e.prefetched != nil && e.prefetched.NoneTypeClass != nil {
				return e.prefetched.NoneTypeClass
			}
			return UnknownTypeCreate(false)
		}

	case *parser.UnaryOperationNode:
		if typed.D.Operator != parser.OperatorTypeSubtract && typed.D.Operator != parser.OperatorTypeAdd {
			return nil
		}
		numberNode, ok := typed.D.Expr.(*parser.NumberNode)
		if !ok || numberNode.D.IsImaginary || !numberNode.D.IsInteger {
			return nil
		}

		value := numberNodeLiteralValue(numberNode.D.Value)
		if typed.D.Operator == parser.OperatorTypeSubtract {
			value = negateIntLiteral(value)
		}
		return e.cloneBuiltinClassWithLiteral(node, classType, "int", value)
	}

	return nil
}

// literalTypeForStringList is the original's string arm. A named unicode escape
// is rejected inside a type expression because its expansion is not stable
// across Python versions.
func (e *typeEvaluator) literalTypeForStringList(
	node *parser.IndexNode,
	classType *ClassType,
	itemExpr *parser.StringListNode,
	flags EvalFlags,
	isValidTypeForm *bool,
) Type {
	strings := []*parser.StringNode{}
	for _, s := range itemExpr.D.Strings {
		stringNode, ok := s.(*parser.StringNode)
		if !ok {
			return nil
		}
		strings = append(strings, stringNode)
	}
	if len(strings) == 0 {
		return nil
	}

	isBytes := (strings[0].D.Token.Flags & parser.StringTokenFlagsBytes) != 0
	value := ""
	for _, s := range strings {
		value += s.D.Value.String()
	}

	builtInName := "str"
	if isBytes {
		builtInName = "bytes"
	}
	t := e.cloneBuiltinClassWithLiteral(node, classType, builtInName, LiteralString(value))

	if (flags & EvalFlagsTypeExpression) != 0 {
		for _, stringNode := range strings {
			if (stringNode.D.Token.Flags & parser.StringTokenFlagsNamedUnicodeEscape) != 0 {
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm,
					localization.LocMessage.LiteralNamedUnicodeEscape(), stringNode, nil)
				*isValidTypeForm = false
			}
		}
	}

	return t
}

// literalTypeFromExpression is the original's fallback: evaluate the argument
// and accept it if it is an enum member or an alias to existing literal types.
func (e *typeEvaluator) literalTypeFromExpression(
	itemExpr parser.ExpressionNode, flags EvalFlags,
) Type {
	exprType := e.GetTypeOfExpression(itemExpr,
		(flags&(EvalFlagsForwardRefs|EvalFlagsTypeExpression))|EvalFlagsNoConvertSpecialForm, nil)

	// The original's comment: is this an enum type?
	if exprClass, ok := exprType.Type.(*ClassType); ok && IsClassInstance(exprType.Type) &&
		ClassTypeIsEnumClass(exprClass) && exprClass.Priv.LiteralValue != nil {
		return ClassTypeCloneAsInstantiable(exprClass, true)
	}

	// The original's comment: is this a type alias to an existing literal type?
	isLiteralType := true
	DoForEachSubtype(exprType.Type, func(subtype Type, _ int, _ []Type) {
		subtypeClass, ok := subtype.(*ClassType)
		if !ok || !IsInstantiableClass(subtype) || subtypeClass.Priv.LiteralValue == nil {
			if !IsNoneTypeClass(subtype) {
				isLiteralType = false
			}
		}
	})

	if isLiteralType {
		return exprType.Type
	}

	return nil
}

// cloneBuiltinClassWithLiteral corresponds to the function of the same name.
func (e *typeEvaluator) cloneBuiltinClassWithLiteral(
	node parser.ParseNode, literalClassType *ClassType, builtInName string, value LiteralValue,
) Type {
	t := e.GetBuiltInType(node, builtInName)
	if IsInstantiableClass(t) {
		literalType := ClassTypeCloneWithLiteral(t.(*ClassType), value)
		literalType.Base().SetSpecialForm(literalClassType)
		return literalType
	}

	return UnknownTypeCreate(false)
}

// negateIntLiteral is the original's `-itemExpr.d.expr.d.value`, which preserves
// the number/bigint arm the value was in.
func negateIntLiteral(value LiteralValue) LiteralValue {
	switch literal := value.(type) {
	case LiteralInt:
		return LiteralInt{Value: new(big.Int).Neg(literal.Value)}
	case LiteralFloat:
		return LiteralFloat(-float64(literal))
	}
	return value
}
