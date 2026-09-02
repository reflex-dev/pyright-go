/*
 * typeevaluator_memberdispatch.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfMemberAccessWithBaseType.
 *
 * The dispatch for `base.member`, keyed on what the base turned out to be. Each
 * category of type answers the question differently, and several of them answer
 * it by transforming the base and recursing rather than by looking anything up.
 *
 *   - Any, Unknown and Never propagate: the member of an unknown thing is
 *     unknown.
 *   - A TYPE VARIABLE is not a thing with members. It is made concrete and the
 *     access re-runs against that, carrying the original along as the self type
 *     so `self.x` inside a method binds to Self rather than to the base class.
 *     A ParamSpec is the exception: `P.args` and `P.kwargs` are real, and legal
 *     only on a parameter of the matching category.
 *   - A CLASS delegates to getTypeOfBoundMember, after giving enums a chance --
 *     an enum member is not an ordinary attribute and `Color.RED` has a literal
 *     type. Writing to one is an error rather than an assignment.
 *   - A MODULE looks in its symbol table, then in `ModuleType` itself (for
 *     synthesized namespace packages), then at a module-level `__getattr__`.
 *     An `Unbound` result is downgraded to Unknown here on purpose: the module
 *     that actually has the problem will report it, and it should not leak into
 *     every importer.
 *   - A UNION maps over its members, with None handled separately so the
 *     diagnostic can say "member of None" rather than "member of the union".
 *   - A FUNCTION has the members of `types.FunctionType` -- or `MethodType` if
 *     it is bound -- so the access re-runs against that class. `__self__` is
 *     special-cased because typeshed types it as plain `object` and the
 *     evaluator knows better.
 *
 * When nothing produced a type, the diagnostic distinguishes access on a
 * function (its own rule, and the result is Any rather than Unknown so that
 * disabling the rule does not cascade into reportUnknownMemberType) from access
 * on anything else, and offers the TypedDict `["key"]` spelling when the name
 * matches a key.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// memberAccessState carries the pieces of the original's mutable local state
// that the category arms write to. The original closes over them directly;
// Go needs them threaded through the helpers the arms became.
type memberAccessState struct {
	resolvedType                Type
	narrowedTypeForSet          Type
	typeErrors                  bool
	isIncomplete                bool
	isAsymmetricAccessor        bool
	memberAccessDeprecationInfo *MemberAccessDeprecationInfo
	diag                        *common.DiagnosticAddendum
}

// getTypeOfMemberAccessWithBaseType corresponds to the function of the same
// name.
func (e *typeEvaluator) getTypeOfMemberAccessWithBaseType(
	node *parser.MemberAccessNode,
	baseTypeResult *TypeResult,
	usage *EvaluatorUsage,
	flags EvalFlags,
) *TypeResult {
	baseType := TransformPossibleRecursiveTypeAlias(baseTypeResult.Type, 0)
	memberName := node.D.Member.D.Value
	fileInfo := GetFileInfo(node)

	st := &memberAccessState{
		isIncomplete: baseTypeResult.IsIncomplete,
		diag:         common.NewDiagnosticAddendum(),
	}

	if usage != nil && usage.SetType != nil && usage.SetType.IsIncomplete {
		st.isIncomplete = true
	}

	// The original's comment: if the base type was incomplete and unbound, don't
	// proceed because false positive errors will be generated.
	if baseTypeResult.IsIncomplete && IsUnbound(baseType) {
		return &TypeResult{Type: UnknownTypeCreate(true), IsIncomplete: true}
	}

	if props := baseType.Base().Props; props != nil && props.SpecialForm != nil &&
		(flags&EvalFlagsTypeExpression) == 0 {
		baseType = props.SpecialForm
	}

	if paramSpec, ok := baseType.(*TypeVarType); ok && IsParamSpec(baseType) &&
		paramSpec.Priv.ParamSpecAccess != ParamSpecAccessNone {
		baseType = e.MakeTopLevelTypeVarsConcrete(baseType, false)
	}

	switch typed := baseType.(type) {
	case *AnyType, *UnknownType, *NeverType:
		st.resolvedType = baseType

	case *UnboundType:
		// Nothing: the error is reported below.

	case *TypeVarType:
		if result := e.memberAccessOnTypeVar(node, typed, memberName, usage, flags, st); result != nil {
			return result
		}

	case *ClassType:
		baseType = e.memberAccessOnClass(node, typed, baseTypeResult, memberName, usage, flags, st)

	case *ModuleType:
		e.memberAccessOnModule(node, typed, memberName, usage, flags, fileInfo, st)

	case *UnionType:
		e.memberAccessOnUnion(node, typed, memberName, usage, baseTypeResult, st)

	case *FunctionType, *OverloadedType:
		e.memberAccessOnFunction(node, baseType, memberName, usage, flags, st)

	default:
		assert(false, "unexpected type category in member access")
	}

	// The original's comment: if type is undefined, emit a general error message
	// indicating that the member could not be accessed.
	if st.resolvedType == nil {
		st.resolvedType = e.reportMemberAccessFailure(node, baseType, memberName, usage, baseTypeResult, st)
	}

	if (flags & EvalFlagsTypeExpression) == 0 {
		e.reportUseOfTypeCheckOnly(st.resolvedType, node.D.Member)
	}

	resolvedType := e.convertSpecialFormToRuntimeValue(st.resolvedType, flags)

	return &TypeResult{
		Type:                        resolvedType,
		IsIncomplete:                st.isIncomplete,
		IsAsymmetricAccessor:        st.isAsymmetricAccessor,
		NarrowedTypeForSet:          st.narrowedTypeForSet,
		MemberAccessDeprecationInfo: st.memberAccessDeprecationInfo,
		TypeErrors:                  st.typeErrors,
	}
	// IsRequired and IsNotRequired are `const ... = false` in the original and
	// are therefore left at their zero values.
}

// memberAccessOnTypeVar is the TypeVar arm. A non-nil return is the original's
// tail-recursive `return getTypeOfMemberAccessWithBaseType(...)`.
func (e *typeEvaluator) memberAccessOnTypeVar(
	node *parser.MemberAccessNode,
	baseType *TypeVarType,
	memberName string,
	usage *EvaluatorUsage,
	flags EvalFlags,
	st *memberAccessState,
) *TypeResult {
	if IsParamSpec(baseType) {
		// The original's comment: handle special cases for "P.args" and "P.kwargs".
		if memberName == "args" || memberName == "kwargs" {
			isArgs := memberName == "args"
			paramNode := GetEnclosingParam(node)
			expectedCategory := parser.ParamCategoryKwargsDict
			access := ParamSpecAccessKwargs
			if isArgs {
				expectedCategory = parser.ParamCategoryArgsList
				access = ParamSpecAccessArgs
			}

			if paramNode == nil || paramNode.D.Category != expectedCategory {
				errorMessage := localization.LocMessage.ParamSpecKwargsUsage()
				if isArgs {
					errorMessage = localization.LocMessage.ParamSpecArgsUsage()
				}
				e.AddDiagnostic(DiagnosticRuleReportInvalidTypeForm, errorMessage, node, nil)
				st.resolvedType = UnknownTypeCreate(st.isIncomplete)
				return nil
			}

			st.resolvedType = TypeVarTypeCloneForParamSpecAccess(baseType, access)
			return nil
		}

		if !st.isIncomplete {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ParamSpecUnknownMember().Format(memberName), node, nil)
		}

		st.resolvedType = UnknownTypeCreate(st.isIncomplete)
		return nil
	}

	// The original's comment: it's illegal to reference a member from a type
	// variable.
	if (flags & EvalFlagsTypeExpression) != 0 {
		if !st.isIncomplete {
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeVarNoMember().Format(e.PrintType(baseType, nil), memberName),
				node.D.LeftExpr, nil)
		}

		st.resolvedType = UnknownTypeCreate(st.isIncomplete)
		return nil
	}

	if baseType.Shared.RecursiveAlias != nil {
		st.resolvedType = UnknownTypeCreate(true)
		st.isIncomplete = true
		return nil
	}

	if IsTypeVarTuple(baseType) {
		return nil
	}

	// A TypeVar has no members of its own. The access re-runs against what it
	// resolves to, carrying the original as the self type so that a method
	// reached this way binds to Self.
	var bindToSelfType Type = baseType
	if baseType.Base().IsInstantiable() {
		bindToSelfType = ConvertToInstance(baseType, false)
	}

	return e.getTypeOfMemberAccessWithBaseType(node, &TypeResult{
		Type:           e.MakeTopLevelTypeVarsConcrete(baseType, false),
		BindToSelfType: bindToSelfType,
		IsIncomplete:   st.isIncomplete,
	}, usage, EvalFlagsNone)
}

// memberAccessOnClass is the Class arm. It returns the base type, which the
// original rebinds for the NewType case and reads again in the failure path.
func (e *typeEvaluator) memberAccessOnClass(
	node *parser.MemberAccessNode,
	baseType *ClassType,
	baseTypeResult *TypeResult,
	memberName string,
	usage *EvaluatorUsage,
	flags EvalFlags,
	st *memberAccessState,
) Type {
	// The original's comment: if this is a class-like function created via
	// NewType, treat it like a function for purposes of member accesses.
	if ClassTypeIsNewTypeClass(baseType) && !baseType.Priv.IncludeSubclasses &&
		e.prefetched != nil && e.prefetched.FunctionClass != nil && IsClass(e.prefetched.FunctionClass) {
		baseType = ClassTypeCloneAsInstance(e.prefetched.FunctionClass.(*ClassType), false)
	}

	var typeResult *TypeResult

	enumMemberResult := GetTypeOfEnumMember(e, node, baseType, memberName, st.isIncomplete)
	if enumMemberResult != nil {
		if usage.Method == "get" {
			typeResult = enumMemberResult
		} else if enumClass, ok := enumMemberResult.Type.(*ClassType); ok &&
			IsClassInstance(enumMemberResult.Type) &&
			ClassTypeIsSameGenericClass(enumClass, ClassTypeCloneAsInstance(baseType, false), 0) &&
			enumClass.Priv.LiteralValue != nil {
			// The original's comment: is this an attempt to delete or overwrite an
			// enum member?
			diagMessage := localization.LocMessage.EnumMemberDelete().Format(memberName)
			if usage.Method == "set" {
				diagMessage = localization.LocMessage.EnumMemberSet().Format(memberName)
			}
			e.AddDiagnostic(DiagnosticRuleReportAttributeAccessIssue,
				diagMessage+st.diag.GetString(),
				node.D.Member, effectiveRangeOr(st.diag, node.D.Member))
		}
	}

	if typeResult == nil {
		memberFlags := MemberAccessFlagsDefault
		if (flags & EvalFlagsTypeExpression) != 0 {
			memberFlags = MemberAccessFlagsTypeExpression
		}
		typeResult = e.GetTypeOfBoundMember(node.D.Member, baseType, memberName, usage,
			st.diag, memberFlags, baseTypeResult.BindToSelfType)
	}

	if typeResult == nil {
		return baseType
	}

	conditionOptions := &AddConditionOptions{SkipSelfCondition: true, SkipBoundTypeVars: true}

	if !typeResult.TypeErrors {
		st.resolvedType = AddConditionToType(typeResult.Type, GetTypeCondition(baseType), conditionOptions)
	} else {
		st.typeErrors = true
	}

	if typeResult.IsAsymmetricAccessor {
		st.isAsymmetricAccessor = true
	}

	if typeResult.IsIncomplete {
		st.isIncomplete = true
	}

	if typeResult.NarrowedTypeForSet != nil {
		st.narrowedTypeForSet = AddConditionToType(typeResult.NarrowedTypeForSet,
			GetTypeCondition(baseType), conditionOptions)
	}

	if typeResult.MemberAccessDeprecationInfo != nil {
		st.memberAccessDeprecationInfo = typeResult.MemberAccessDeprecationInfo
	}

	return baseType
}

// memberAccessOnModule is the Module arm.
func (e *typeEvaluator) memberAccessOnModule(
	node *parser.MemberAccessNode,
	baseType *ModuleType,
	memberName string,
	usage *EvaluatorUsage,
	flags EvalFlags,
	fileInfo *AnalyzerFileInfo,
	st *memberAccessState,
) {
	symbol := ModuleTypeGetField(baseType, memberName)

	// The original's comment: if the symbol isn't found in the module's symbol
	// table, see if it's defined in the `ModuleType` class. This is needed for
	// modules that are synthesized for namespace packages.
	if symbol == nil && e.prefetched != nil && e.prefetched.ModuleTypeClass != nil &&
		IsInstantiableClass(e.prefetched.ModuleTypeClass) {
		if found, ok := ClassTypeGetSymbolTable(e.prefetched.ModuleTypeClass.(*ClassType)).Get(memberName); ok {
			symbol = found
		}
	}

	if symbol != nil && !symbol.IsExternallyHidden() {
		e.moduleMemberFromSymbol(node, baseType, symbol, memberName, usage, flags, fileInfo, st)
		return
	}

	// The original's comment: does the module export a top-level __getattr__
	// function?
	if usage.Method == "get" {
		if getAttrSymbol := ModuleTypeGetField(baseType, "__getattr__"); getAttrSymbol != nil {
			if e.moduleGetAttrSupported(getAttrSymbol, fileInfo) {
				getAttrTypeResult := e.GetEffectiveTypeOfSymbolForUsage(getAttrSymbol, nil, false)
				if fn, ok := getAttrTypeResult.Type.(*FunctionType); ok {
					returnTypeResult := e.getEffectiveReturnTypeResult(fn, nil)
					st.resolvedType = returnTypeResult.Type
					if getAttrTypeResult.IsIncomplete || returnTypeResult.IsIncomplete {
						st.isIncomplete = true
					}
				}
			}
		}
	}

	// The original's comment: if the field was not found and the module type is
	// marked such that all fields should be Any/Unknown, return that type.
	if st.resolvedType == nil && baseType.Priv.NotPresentFieldType != nil {
		st.resolvedType = baseType.Priv.NotPresentFieldType
	}

	if st.resolvedType == nil {
		if !st.isIncomplete {
			e.AddDiagnostic(DiagnosticRuleReportAttributeAccessIssue,
				localization.LocMessage.ModuleUnknownMember().Format(memberName, baseType.Priv.ModuleName),
				node.D.Member, nil)
		}
		if e.evaluatorOptions.EvaluateUnknownImportsAsAny {
			st.resolvedType = AnyTypeCreate(false)
		} else {
			st.resolvedType = UnknownTypeCreate(false)
		}
	}
}

// moduleGetAttrSupported is the original's PEP 562 version gate. A stub file may
// use it regardless of version, and so may a symbol declared in one.
func (e *typeEvaluator) moduleGetAttrSupported(getAttrSymbol *Symbol, fileInfo *AnalyzerFileInfo) bool {
	if fileInfo.ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_7) {
		return true
	}
	for _, decl := range getAttrSymbol.GetDeclarations() {
		if decl.DeclBase().Uri.HasExtension(".pyi") {
			return true
		}
	}
	return false
}

// moduleMemberFromSymbol is the original's `symbol && !symbol.isExternallyHidden()`
// branch.
func (e *typeEvaluator) moduleMemberFromSymbol(
	node *parser.MemberAccessNode,
	baseType *ModuleType,
	symbol *Symbol,
	memberName string,
	usage *EvaluatorUsage,
	flags EvalFlags,
	fileInfo *AnalyzerFileInfo,
	st *memberAccessState,
) {
	if usage.Method == "get" {
		e.setSymbolAccessed(fileInfo, symbol, node.D.Member)
	}

	typeResult := e.GetEffectiveTypeOfSymbolForUsage(symbol, nil, true)
	resolvedType := typeResult.Type

	if (flags & EvalFlagsTypeExpression) != 0 {
		resolvedType = e.validateSymbolIsTypeExpression(node, resolvedType, typeResult.IncludesVariableDecl)
	}

	// The original's comment: add TypeForm details if appropriate.
	resolvedType = e.addTypeFormForSymbol(node, resolvedType, flags, typeResult.IncludesVariableDecl)

	if IsTypeVar(resolvedType) {
		resolvedType = e.validateTypeVarUsage(node, resolvedType, flags)
	}

	// The original's comment: if the type resolved to "unbound", treat it as
	// "unknown" in the case of a module reference because if it's truly unbound,
	// that error will be reported within the module and should not leak into other
	// modules that import it.
	if IsUnbound(resolvedType) {
		resolvedType = UnknownTypeCreate(true)
	}

	st.resolvedType = resolvedType

	if symbol.IsPrivateMember() {
		e.AddDiagnostic(DiagnosticRuleReportPrivateUsage,
			localization.LocMessage.PrivateUsedOutsideOfModule().Format(memberName), node.D.Member, nil)
	}

	if symbol.IsPrivatePyTypedImport() {
		e.AddDiagnostic(DiagnosticRuleReportPrivateImportUsage,
			localization.LocMessage.PrivateImportFromPyTypedModule().Format(memberName, baseType.Priv.ModuleName),
			node.D.Member, nil)
	}
}

// memberAccessOnUnion is the Union arm.
func (e *typeEvaluator) memberAccessOnUnion(
	node *parser.MemberAccessNode,
	baseType *UnionType,
	memberName string,
	usage *EvaluatorUsage,
	baseTypeResult *TypeResult,
	st *memberAccessState,
) {
	st.resolvedType = MapSubtypes(baseType, func(subtype Type) Type {
		if IsUnbound(subtype) {
			// The original's comment: don't do anything if it's unbound. The error
			// will already be reported elsewhere.
			return nil
		}

		if IsNoneInstance(subtype) {
			assert(IsClassInstance(subtype), "expected a class instance")
			typeResult := e.GetTypeOfBoundMember(node.D.Member, subtype.(*ClassType), memberName,
				usage, st.diag, MemberAccessFlagsDefault, nil)

			if typeResult != nil && !typeResult.TypeErrors {
				noneMemberType := AddConditionToType(typeResult.Type, GetTypeCondition(baseType),
					&AddConditionOptions{SkipBoundTypeVars: true})
				if typeResult.IsIncomplete {
					st.isIncomplete = true
				}

				// The original assigns to the outer `type` here as well as
				// returning; the assignment is dead because mapSubtypes overwrites
				// it, so only the return is reproduced.
				return noneMemberType
			}

			if !st.isIncomplete {
				e.AddDiagnostic(DiagnosticRuleReportOptionalMemberAccess,
					localization.LocMessage.NoneUnknownMember().Format(memberName), node.D.Member, nil)
			}

			return nil
		}

		typeResult := e.getTypeOfMemberAccessWithBaseType(node, &TypeResult{
			Type:         subtype,
			IsIncomplete: baseTypeResult.IsIncomplete,
		}, usage, EvalFlagsNone)

		if typeResult.IsIncomplete {
			st.isIncomplete = true
		}

		if typeResult.MemberAccessDeprecationInfo != nil {
			st.memberAccessDeprecationInfo = typeResult.MemberAccessDeprecationInfo
		}

		if typeResult.TypeErrors {
			st.typeErrors = true
		}

		return typeResult.Type
	}, nil)
}

// memberAccessOnFunction is the Function/Overloaded arm. A function's members
// are those of `types.FunctionType`, or `MethodType` when it is bound.
func (e *typeEvaluator) memberAccessOnFunction(
	node *parser.MemberAccessNode,
	baseType Type,
	memberName string,
	usage *EvaluatorUsage,
	flags EvalFlags,
	st *memberAccessState,
) {
	hasSelf := IsMethodType(baseType)

	if memberName == "__self__" && hasSelf {
		// The original's comment: handle "__self__" specially because MethodType
		// defines it simply as "object". We can do better here.
		var functionType *FunctionType

		if fn, ok := baseType.(*FunctionType); ok {
			functionType = fn
		} else if overloads := OverloadedTypeGetOverloads(baseType.(*OverloadedType)); len(overloads) > 0 {
			functionType = overloads[0]
		}

		if functionType != nil && functionType.Priv.BoundToType != nil {
			st.resolvedType = functionType.Priv.BoundToType
		}
		return
	}

	var altType Type
	if e.prefetched != nil {
		if hasSelf {
			altType = e.prefetched.MethodClass
		} else {
			altType = e.prefetched.FunctionClass
		}
	}

	var altInstance Type = UnknownTypeCreate(false)
	if altType != nil {
		altInstance = ConvertToInstance(altType, false)
	}

	st.resolvedType = e.getTypeOfMemberAccessWithBaseType(node,
		&TypeResult{Type: altInstance}, usage, flags).Type
}

// reportMemberAccessFailure is the original's `if (!type)` tail.
func (e *typeEvaluator) reportMemberAccessFailure(
	node *parser.MemberAccessNode,
	baseType Type,
	memberName string,
	usage *EvaluatorUsage,
	baseTypeResult *TypeResult,
	st *memberAccessState,
) Type {
	isFunctionRule := IsFunctionOrOverloaded(baseType)
	if !isFunctionRule && IsClassInstance(baseType) {
		isFunctionRule = ClassTypeIsBuiltInNamed(baseType.(*ClassType), "function", "FunctionType")
	}

	if !baseTypeResult.IsIncomplete {
		diagMessage := localization.LocMessage.MemberAccess().Format(memberName, e.PrintType(baseType, nil))
		switch usage.Method {
		case "set":
			diagMessage = localization.LocMessage.MemberSet().Format(memberName, e.PrintType(baseType, nil))
		case "del":
			diagMessage = localization.LocMessage.MemberDelete().Format(memberName, e.PrintType(baseType, nil))
		}

		diag := st.diag

		// The original's comment: if there is an expected type diagnostic addendum
		// (used for assignments), use that rather than the local diagnostic addendum
		// because it will be more informative.
		if usage.SetExpectedTypeDiag != nil && !usage.SetExpectedTypeDiag.IsEmpty() {
			diag = usage.SetExpectedTypeDiag
		}

		// The original's comment: if the class is a TypedDict, and there's a key with
		// the same name, suggest that they user want to use ["key"] name instead.
		if cls, ok := baseType.(*ClassType); ok && IsClass(baseType) &&
			cls.Shared.TypedDictEntries != nil {
			if _, found := cls.Shared.TypedDictEntries.KnownItems.Get(memberName); found {
				subDiag := common.NewDiagnosticAddendum()
				subDiag.AddMessage(localization.LocAddendum.TypedDictKeyAccess().Format(memberName))
				diag.AddAddendum(subDiag)
			}
		}

		rule := DiagnosticRuleReportAttributeAccessIssue
		if isFunctionRule {
			rule = DiagnosticRuleReportFunctionMemberAccess
		}

		e.AddDiagnostic(rule,
			diagMessage+diag.GetString(),
			node.D.Member, effectiveRangeOr(diag, node.D.Member))
	}

	// The original's comment: if this is member access on a function, use "Any" so
	// if the reportFunctionMemberAccess rule is disabled, we don't trigger
	// additional reportUnknownMemberType diagnostics.
	if isFunctionRule {
		return AnyTypeCreate(false)
	}
	return UnknownTypeCreate(false)
}

// effectiveRangeOr is the original's `diag.getEffectiveTextRange() ?? node`,
// which the addendum answers only when it carries a range of its own.
func effectiveRangeOr(diag *common.DiagnosticAddendum, node parser.ParseNode) *common.TextRange {
	if r := diag.GetEffectiveTextRange(); r != nil {
		return r
	}
	textRange := node.NodeBase().TextRange
	return &textRange
}
