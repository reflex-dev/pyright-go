/*
 * typeevaluator_class.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfClass and the helpers that exist only to serve it.
 *
 * This is class creation, and it is the thing the rest of the evaluator has
 * been waiting on. Every path ported so far -- getType, the context walk, the
 * expression dispatch, getTypeOfName, symbol lookup, the effective-type fork,
 * declaration resolution, the prefetch bootstrap -- terminates here, because a
 * name resolves to a symbol, a symbol resolves to a declaration, and a class
 * declaration resolves by building the class.
 *
 * The shape of the original is one long function that mutates a
 * partially-evaluated ClassType in place. That is not incidental: the class is
 * written to the type cache and registered as a partial resolution *before* its
 * base classes are evaluated, because base class expressions can refer to the
 * class being defined. The port keeps the single mutating pass rather than
 * splitting it into stages, since the ordering is the semantics.
 *
 * The body of the original is wrapped in invalidateTypeCacheIfCanceled, which
 * marks a cancellation exception so the caller knows the type cache holds a
 * partially-constructed class. Cancellation is not carried by this port (see
 * the header of typeevaluatortypes.go), so the wrapper is a defer that would
 * have somewhere to hang that flag; it is written out rather than dropped so
 * the reason it does nothing is visible.
 *
 * The five satellites this reaches -- applyClassDecorator (decorators.ts),
 * synthesizeTypedDictClassMethods (typedDicts.ts), synthesizeDataClassMethods,
 * synthesizeDataClassSlots and applyDataClassClassBehaviorOverrides
 * (dataClasses.ts) -- are all ported; each is a thin delegation below.
 */

package analyzer

import (
	"fmt"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// typePromotions corresponds to the module-level constant of the same name: the
// PEP 484 implicit numeric tower plus the bytes promotions.
var typePromotions = map[string][]string{
	"builtins.float":   {"builtins.int"},
	"builtins.complex": {"builtins.float", "builtins.int"},
	"builtins.bytes":   {"builtins.bytearray", "builtins.memoryview"},
}

// GetTypeOfClass corresponds to getTypeOfClass. It returns nil where the
// original returns undefined.
func (e *typeEvaluator) GetTypeOfClass(node *parser.ClassNode) *ClassTypeResult {
	if node == nil {
		return nil
	}

	e.initializePrefetchedTypes(node)

	// Is this type already cached?
	if cachedClassType := e.readTypeCache(node.D.Name, evalFlagsNonePtr()); cachedClassType != nil {
		if !IsInstantiableClass(cachedClassType) {
			// The original's comment: this can happen in rare circumstances
			// where the class declaration is located in an unreachable code
			// block.
			return nil
		}

		decoratedType := e.readTypeCache(node, evalFlagsNonePtr())
		if decoratedType == nil {
			decoratedType = UnknownTypeCreate(false)
		}

		return &ClassTypeResult{
			ClassType:     cachedClassType.(*ClassType),
			DecoratedType: decoratedType,
		}
	}

	// The original's comment: the type wasn't cached, so we need to create a
	// new one.
	scope := GetScopeForNode(node)

	fileInfo := GetFileInfo(node)
	classFlags := ClassTypeFlagsNone
	if (scope != nil && scope.Type == ScopeTypeBuiltin) ||
		fileInfo.IsTypingStubFile ||
		fileInfo.IsTypingExtensionsStubFile ||
		fileInfo.IsBuiltInStubFile ||
		fileInfo.IsTypeshedStubFile {
		classFlags |= ClassTypeFlagsBuiltIn

		if fileInfo.IsTypingExtensionsStubFile {
			classFlags |= ClassTypeFlagsTypingExtensionClass
		}

		if node.D.Name.D.Value == "property" {
			classFlags |= ClassTypeFlagsPropertyClass
		}

		if node.D.Name.D.Value == "tuple" {
			classFlags |= ClassTypeFlagsTupleClass
		}
	}

	if fileInfo.IsStubFile {
		classFlags |= ClassTypeFlagsDefinedInStub
	}

	docString, hasDocString := GetDocString(node.D.Suite.D.Statements)
	var docStringPtr *string
	if hasDocString {
		docStringPtr = &docString
	}

	classType := ClassTypeCreateInstantiable(
		node.D.Name.D.Value,
		GetClassFullName(node, fileInfo.ModuleName, node.D.Name.D.Value),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		classFlags,
		GetTypeSourceID(node),
		nil,
		nil,
		docStringPtr,
	)

	classType.Shared.TypeVarScopeID = GetScopeIdForNode(node)

	// The original's comment: is this a special type that supports type
	// promotions according to PEP 484?
	if _, ok := typePromotions[classType.Shared.FullName]; ok {
		includePromotions := true
		classType.Priv.IncludePromotions = &includePromotions
	}

	// The original's comment: some classes refer to themselves within type
	// arguments used within base classes. We'll register the
	// partially-constructed class type to allow these to be resolved.
	var classSymbol *Symbol
	if scope != nil {
		classSymbol = scope.LookUpSymbol(node.D.Name.D.Value)
	}

	var classDecl Declaration
	if decl := GetDeclaration(node); decl != nil {
		classDecl = decl
	}

	if classDecl != nil && classSymbol != nil {
		e.setSymbolResolutionPartialType(classSymbol, classDecl, classType)
	}

	classType.Shared.Flags |= ClassTypeFlagsPartiallyEvaluated
	classType.Shared.Declaration = classDecl

	return e.invalidateTypeCacheIfCanceled(func() *ClassTypeResult {
		return e.buildClassType(node, classType, fileInfo)
	})
}

// buildClassType is the body of the original's invalidateTypeCacheIfCanceled
// callback.
func (e *typeEvaluator) buildClassType(
	node *parser.ClassNode,
	classType *ClassType,
	fileInfo *AnalyzerFileInfo,
) *ClassTypeResult {
	e.writeTypeCache(node, &TypeResult{Type: classType}, nil, nil, false)
	e.writeTypeCache(node.D.Name, &TypeResult{Type: classType}, nil, nil, false)

	// The original's comment: keep a list of unique type parameters that are
	// used in the base class arguments.
	var typeParams []*TypeVarType

	if node.D.TypeParams != nil {
		for _, t := range e.evaluateTypeParamList(node.D.TypeParams) {
			typeParams = append(typeParams, TypeVarTypeCloneAsInstance(t))
		}
	}

	// The original's comment: if the class derives from "Generic" directly, it
	// will provide all of the type parameters in the specified order.
	var genericTypeParams []*TypeVarType
	var protocolTypeParams []*TypeVarType
	sawGenericTypeParams := false
	sawProtocolTypeParams := false
	isNamedTupleSubclass := false

	var initSubclassArgs []*Arg
	var metaclassNode parser.ExpressionNode

	exprFlags := EvalFlagsInstantiableType |
		EvalFlagsAllowGeneric |
		EvalFlagsNoNakedGeneric |
		EvalFlagsNoTypeVarWithScopeId |
		EvalFlagsTypeVarGetsCurScope |
		EvalFlagsEnforceVarianceConsistency
	if fileInfo.IsStubFile {
		exprFlags |= EvalFlagsForwardRefs
	}
	sawClosedOrExtraItems := false

	for _, arg := range node.D.Arguments {
		// The original's comment: ignore unpacked arguments.
		if arg.D.ArgCategory == parser.ArgCategoryUnpackedDictionary {
			// The original's comment: evaluate the expression's type so symbols
			// are marked accessed and errors are reported.
			e.getTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil)
			continue
		}

		if arg.D.Name == nil {
			e.processClassBaseArg(
				node, classType, fileInfo, arg, exprFlags,
				&typeParams, &genericTypeParams, &protocolTypeParams,
				&sawGenericTypeParams, &sawProtocolTypeParams, &isNamedTupleSubclass,
			)
			continue
		}

		if ClassTypeIsTypedDictClass(classType) {
			e.processTypedDictClassArg(classType, fileInfo, arg, &sawClosedOrExtraItems)
			continue
		}

		if arg.D.Name.D.Value == "metaclass" {
			if metaclassNode != nil {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.MetaclassDuplicate(), arg, nil)
			} else {
				metaclassNode = arg.D.ValueExpr
			}
			continue
		}

		// The original's comment: collect arguments that will be passed to the
		// `__init_subclass__` method described in PEP 487.
		initSubclassArgs = append(initSubclassArgs, &Arg{
			ArgCategory:     parser.ArgCategorySimple,
			Node:            arg,
			Name:            arg.D.Name,
			ValueExpression: arg.D.ValueExpr,
		})
	}

	// The original's comment: check for NamedTuple multiple inheritance.
	if len(classType.Shared.BaseClasses) > 1 {
		derivesFromNamedTuple := false
		foundIllegalBaseClass := false

		for _, baseClass := range classType.Shared.BaseClasses {
			if IsInstantiableClass(baseClass) {
				bc := baseClass.(*ClassType)
				if ClassTypeIsBuiltInNamed(bc, "NamedTuple") {
					derivesFromNamedTuple = true
				} else if !ClassTypeIsBuiltInNamed(bc, "Generic") {
					foundIllegalBaseClass = true
				}
			}
		}

		if derivesFromNamedTuple && foundIllegalBaseClass {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.NamedTupleMultipleInheritance(),
				node.D.Name,
				nil,
			)
		}
	}

	// The original's comment: make sure we don't have 'object' derive from
	// itself. Infinite recursion will result.
	if !ClassTypeIsBuiltInNamed(classType, "object") {
		knownBaseCount := 0
		for _, baseClass := range classType.Shared.BaseClasses {
			if IsClass(baseClass) {
				knownBaseCount++
			}
		}
		if knownBaseCount == 0 {
			// The original's comment: if there are no other (known) base
			// classes, the class implicitly derives from object.
			classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, e.GetBuiltInType(node, "object"))
		}
	}

	// The original's comment: if genericTypeParams or protocolTypeParams are
	// provided, make sure that typeParams is a proper subset.
	if !sawGenericTypeParams && sawProtocolTypeParams {
		genericTypeParams = protocolTypeParams
		sawGenericTypeParams = true
	}
	if sawGenericTypeParams && node.D.TypeParams == nil {
		e.verifyGenericTypeParams(node.D.Name, typeParams, genericTypeParams)
	}
	if sawGenericTypeParams {
		classType.Shared.TypeParams = genericTypeParams
	} else {
		classType.Shared.TypeParams = typeParams
	}

	// The original's comment: determine if one or more type parameters is
	// autovariance.
	for _, param := range classType.Shared.TypeParams {
		if param.Shared.DeclaredVariance == VarianceAuto && param.Priv.ComputedVariance == nil {
			classType.Shared.RequiresVarianceInference = true
			break
		}
	}

	e.checkClassVariadics(node, typeParams)

	// The original's comment: validate the default types for all type
	// parameters.
	for index, typeParam := range classType.Shared.TypeParams {
		var bestErrorNode parser.ExpressionNode = node.D.Name
		if node.D.TypeParams != nil && index < len(node.D.TypeParams.D.Params) {
			typeParamNode := node.D.TypeParams.D.Params[index]
			if typeParamNode.D.DefaultExpr != nil {
				bestErrorNode = typeParamNode.D.DefaultExpr
			} else {
				bestErrorNode = typeParamNode.D.Name
			}
		}
		e.validateTypeParamDefault(
			bestErrorNode,
			typeParam,
			classType.Shared.TypeParams[:index],
			classType.Shared.TypeVarScopeID,
		)
	}

	if !ComputeMroLinearization(classType) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.MethodOrdering(), node.D.Name, nil)
	}

	// The original's comment: the scope for this class becomes the "fields" for
	// the corresponding type.
	innerScope := GetScopeForNode(node.D.Suite)
	if innerScope != nil && innerScope.SymbolTable != nil {
		classType.Shared.Fields = innerScope.SymbolTable.Clone()
	} else {
		classType.Shared.Fields = NewSymbolTable()
	}

	// The original's comment: determine whether the class should inherit
	// __hash__. If a class defines __eq__ but doesn't define __hash__ then
	// __hash__ is set to None.
	if _, hasEq := classType.Shared.Fields.Get("__eq__"); hasEq {
		if _, hasHash := classType.Shared.Fields.Get("__hash__"); !hasHash {
			classType.Shared.Fields.Set("__hash__", SymbolCreateWithType(
				SymbolFlagsClassMember|
					SymbolFlagsClassVar|
					SymbolFlagsIgnoredForProtocolMatch|
					SymbolFlagsIgnoredForOverrideChecks,
				e.GetNoneType(),
				nil,
			))
		}
	}

	// The original's comment: determine whether the class's instance variables
	// are constrained to those defined by __slots__. We need to do this prior to
	// dataclass processing because dataclasses can implicitly add to the slots
	// list.
	if innerScope != nil {
		if slotsNames := innerScope.GetSlotsNames(); slotsNames != nil {
			classType.Shared.LocalSlotsNames = slotsNames
		}
		classType.Shared.HasNonEmptySlots = innerScope.HasNonEmptySlots
	}

	e.applyPseudoGenericTypeParams(node, classType, fileInfo)

	// The original's comment: determine if the class has a custom
	// __class_getitem__ method. This applies only to classes that have no type
	// parameters, since those with type parameters are assumed to follow normal
	// subscripting semantics for generic classes.
	if len(classType.Shared.TypeParams) == 0 && !ClassTypeIsBuiltInNamed(classType, "type") {
		hasCustom := false
		for _, baseClass := range classType.Shared.BaseClasses {
			if IsInstantiableClass(baseClass) && ClassTypeHasCustomClassGetItem(baseClass.(*ClassType)) {
				hasCustom = true
				break
			}
		}
		if !hasCustom {
			_, hasCustom = classType.Shared.Fields.Get("__class_getitem__")
		}
		if hasCustom {
			classType.Shared.Flags |= ClassTypeFlagsHasCustomClassGetItem
		}
	}

	e.applyDeclaredMetaclass(classType, metaclassNode, exprFlags)

	effectiveMetaclass := e.computeEffectiveMetaclass(classType, node.D.Name)
	e.applyEnumFlags(node, classType, effectiveMetaclass)

	// The original's comment: clear the "partially constructed" flag.
	classType.Shared.Flags &^= ClassTypeFlagsPartiallyEvaluated

	decoratedType := e.applyClassDecorators(node, classType)

	e.applyDataClassBehaviors(node, classType, effectiveMetaclass, initSubclassArgs)

	// The original's comment: run any deferred class completions that depend on
	// this class.
	e.runDeferredClassCompletions(classType)

	// The original's comment: if there are any outstanding deferred class
	// completions registered that were not removed by the call to
	// runDeferredClassCompletions, assume that the current class may depend on
	// them and register for deferred completion.
	e.registerDeferredClassCompletion(node, nil)

	e.synthesizeTypedDictIfNeeded(node, classType)
	e.synthesizeDataClassIfNeeded(node, classType, isNamedTupleSubclass)

	// The original's comment: build a complete list of all slots names defined
	// by the class hierarchy. This needs to be done after dataclass processing.
	classType.Shared.CalculateInheritedSlotsNamesDeferred = func() {
		classType.Shared.CalculateInheritedSlotsNamesDeferred = nil
		e.calculateInheritedSlotsNames(classType)
	}

	// The original's comment: if Any is defined using a class statement, treat
	// it as a special form.
	if node.D.Name.D.Value == "Any" && fileInfo.IsTypingStubFile {
		decoratedType = AnyTypeCreateSpecialForm()
	}

	// The original's comment: update the undecorated class type.
	e.writeTypeCache(node.D.Name, &TypeResult{Type: classType}, evalFlagsNonePtr(), nil, false)

	// The original's comment: update the decorated class type.
	e.writeTypeCache(node, &TypeResult{Type: decoratedType}, evalFlagsNonePtr(), nil, false)

	return &ClassTypeResult{ClassType: classType, DecoratedType: decoratedType}
}

/*
 * The base class argument arm, lifted out because the original's forEach body
 * is several hundred lines.
 */

func (e *typeEvaluator) processClassBaseArg(
	node *parser.ClassNode,
	classType *ClassType,
	fileInfo *AnalyzerFileInfo,
	arg *parser.ArgumentNode,
	exprFlags EvalFlags,
	typeParams *[]*TypeVarType,
	genericTypeParams *[]*TypeVarType,
	protocolTypeParams *[]*TypeVarType,
	sawGenericTypeParams *bool,
	sawProtocolTypeParams *bool,
	isNamedTupleSubclass *bool,
) {
	var argType Type

	if arg.D.ArgCategory == parser.ArgCategoryUnpackedList {
		e.getTypeOfExpression(arg.D.ValueExpr, EvalFlagsNone, nil)
		argType = UnknownTypeCreate(false)
	} else {
		argType = e.getTypeOfExpression(arg.D.ValueExpr, exprFlags, nil).Type

		if IsTypeVar(argType) {
			if props := argType.Base().Props; props != nil && props.SpecialForm != nil &&
				props.SpecialForm.Base().IsInstance() {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.BaseClassInvalid(), arg, nil)
				argType = UnknownTypeCreate(false)
			}
		}

		argType = e.MakeTopLevelTypeVarsConcrete(argType, false)
	}

	// The original's comment: in some stub files, classes are conditionally
	// defined (e.g. based on platform type). We'll assume that the conditional
	// logic is correct and strip off the "unbound" union.
	if IsUnion(argType) {
		argType = RemoveUnbound(argType)
	}

	// The original's comment: Any is allowed as a base class. Remove its
	// "special form" flag to avoid false positive errors.
	if props := argType.Base().Props; IsAny(argType) && props != nil && props.SpecialForm != nil {
		argType = AnyTypeCreate(false)
	}

	argType = StripTypeFormRecursive(argType, 0)

	if !IsAnyOrUnknown(argType) && !IsUnbound(argType) {
		argType = e.adjustBaseClassArgType(node, classType, fileInfo, arg, argType)
	}

	if IsUnknown(argType) {
		e.AddDiagnostic(DiagnosticRuleReportUntypedBaseClass, localization.LocMessage.BaseClassUnknown(), arg, nil)
	}

	// The original's comment: check for a duplicate class.
	for _, prevBaseClass := range classType.Shared.BaseClasses {
		if IsInstantiableClass(prevBaseClass) && IsInstantiableClass(argType) &&
			ClassTypeIsSameGenericClass(argType.(*ClassType), prevBaseClass.(*ClassType), 0) {
			var errorNode parser.ParseNode = arg
			if arg.D.Name != nil {
				errorNode = arg.D.Name
			}
			e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.DuplicateBaseClass(), errorNode, nil)
			break
		}
	}

	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, argType)

	if IsInstantiableClass(argType) {
		bc := argType.(*ClassType)
		if ClassTypeIsEnumClass(bc) {
			classType.Shared.Flags |= ClassTypeFlagsEnumClass
		}

		// The original's comment: determine if the class is abstract. Protocol
		// classes support abstract methods because they are constructed by the
		// _ProtocolMeta metaclass, which derives from ABCMeta.
		if ClassTypeSupportsAbstractMethods(bc) || ClassTypeIsProtocolClass(bc) {
			classType.Shared.Flags |= ClassTypeFlagsSupportsAbstractMethods
		}

		if ClassTypeIsPropertyClass(bc) {
			classType.Shared.Flags |= ClassTypeFlagsPropertyClass
		}

		if ClassTypeIsFinal(bc) {
			className := e.printObjectTypeForClass(bc)
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.BaseClassFinal().Format(className),
				arg.D.ValueExpr,
				nil,
			)
		}
	}

	*typeParams = AddTypeVarsToListIfUnique(*typeParams, GetTypeVarArgsRecursive(argType, 0), "")

	if !IsInstantiableClass(argType) {
		return
	}

	bc := argType.(*ClassType)
	if ClassTypeIsBuiltInNamed(bc, "Generic") {
		// The original's comment: 'Generic' is implicitly added if type
		// parameter syntax is used.
		if node.D.TypeParams != nil {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.GenericBaseClassNotAllowed(),
				arg.D.ValueExpr,
				nil,
			)
			return
		}

		if *sawGenericTypeParams {
			return
		}

		if *sawProtocolTypeParams {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.DuplicateGenericAndProtocolBase(),
				arg.D.ValueExpr,
				nil,
			)
		}
		*genericTypeParams = buildTypeParamsFromTypeArgs(bc)
		*sawGenericTypeParams = true
		return
	}

	if ClassTypeIsBuiltInNamed(bc, "Protocol") && len(bc.Priv.TypeArgs) > 0 {
		if *sawProtocolTypeParams {
			return
		}

		if *sawGenericTypeParams {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.DuplicateGenericAndProtocolBase(),
				arg.D.ValueExpr,
				nil,
			)
		}
		*protocolTypeParams = buildTypeParamsFromTypeArgs(bc)
		*sawProtocolTypeParams = true

		if node.D.TypeParams != nil && len(*protocolTypeParams) > 0 {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ProtocolBaseClassWithTypeArgs(),
				arg.D.ValueExpr,
				nil,
			)
			*protocolTypeParams = []*TypeVarType{}
		}
	}
}

// adjustBaseClassArgType is the original's `if (!isAnyOrUnknown(argType) &&
// !isUnbound(argType))` block: the base class is a real type, so it is checked
// and possibly replaced.
func (e *typeEvaluator) adjustBaseClassArgType(
	node *parser.ClassNode,
	classType *ClassType,
	fileInfo *AnalyzerFileInfo,
	arg *parser.ArgumentNode,
	argType Type,
) Type {
	// The original's comment: if the specified base class is type(T), use the
	// metaclass of T if it's known.
	if IsClass(argType) && argType.Base().GetInstantiableDepth() > 0 {
		if meta := argType.(*ClassType).Shared.EffectiveMetaclass; meta != nil && IsClass(meta) {
			argType = meta
		}
	}

	if IsMetaclassInstance(argType) {
		instance := argType.(*ClassType)
		if len(instance.Priv.TypeArgs) > 0 {
			return instance.Priv.TypeArgs[0]
		}
		return UnknownTypeCreate(false)
	}

	if !IsInstantiableClass(argType) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.BaseClassInvalid(), arg, nil)
		return UnknownTypeCreate(false)
	}

	bc := argType.(*ClassType)

	partiallyEvaluated := ClassTypeIsPartiallyEvaluated(bc)
	if !partiallyEvaluated {
		for _, t := range bc.Shared.Mro {
			if IsClass(t) && ClassTypeIsPartiallyEvaluated(t.(*ClassType)) {
				partiallyEvaluated = true
				break
			}
		}
	}
	if partiallyEvaluated {
		// The original's comment: if the base class is partially evaluated,
		// install a callback so we can fix up this class (e.g. compute the MRO)
		// when the dependent class is completed.
		e.registerDeferredClassCompletion(node, bc)
	}

	if ClassTypeIsBuiltInNamed(bc, "Protocol") {
		if !fileInfo.IsStubFile && !ClassTypeIsTypingExtensionClass(bc) &&
			fileInfo.ExecutionEnvironment.PythonVersion.IsLessThan(common.PythonVersion3_7) {
			e.AddDiagnostic(
				DiagnosticRuleReportInvalidTypeForm,
				localization.LocMessage.ProtocolIllegal(),
				arg.D.ValueExpr,
				nil,
			)
		}
		classType.Shared.Flags |= ClassTypeFlagsProtocolClass
	}

	if ClassTypeIsBuiltInNamed(bc, "property") {
		classType.Shared.Flags |= ClassTypeFlagsPropertyClass
	}

	// The original's comment: if the class directly derives from NamedTuple (in
	// Python 3.6 or newer), it's considered a (read-only) dataclass. The caller
	// records the result; this function only reports it through the return of
	// isNamedTupleBase below.
	_ = node

	// The original's comment: if the class directly derives from TypedDict or
	// from a class that is a TypedDict, it is considered a TypedDict.
	if ClassTypeIsBuiltInNamed(bc, "TypedDict") || ClassTypeIsTypedDictClass(bc) {
		classType.Shared.Flags |= ClassTypeFlagsTypedDictClass

		// The original's comment: propagate the "effectively closed" flag from
		// base classes.
		if ClassTypeIsTypedDictEffectivelyClosed(bc) {
			classType.Shared.Flags |= ClassTypeFlagsTypedDictEffectivelyClosed
		}
	}

	// The original's comment: validate that the class isn't deriving from
	// itself, creating a circular dependency.
	if DerivesFromClassRecursive(bc, classType, true) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.BaseClassCircular(), arg, nil)
		return UnknownTypeCreate(false)
	}

	// The original's comment: if the class is attempting to derive from a
	// TypeAliasType, generate an error.
	if props := bc.Base().Props; props != nil && props.SpecialForm != nil &&
		ClassTypeIsBuiltInNamed(props.SpecialForm, "TypeAliasType") {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.TypeAliasTypeBaseClass(), arg, nil)
		return UnknownTypeCreate(false)
	}

	return argType
}

/*
 * The TypedDict keyword-argument arm.
 */

func (e *typeEvaluator) processTypedDictClassArg(
	classType *ClassType,
	fileInfo *AnalyzerFileInfo,
	arg *parser.ArgumentNode,
	sawClosedOrExtraItems *bool,
) {
	name := arg.D.Name.D.Value

	switch name {
	case "total", "closed":
		// The original's comment: the "total" and "readonly" parameters apply
		// only for TypedDict classes. PEP 589 specifies that the parameter must
		// be either True or False.
		// The original passes only three arguments; the two alias lists are
		// optional there and absent here.
		constArgValue, known := EvaluateStaticBoolExpression(
			arg.D.ValueExpr,
			fileInfo.ExecutionEnvironment,
			fileInfo.DefinedConstants,
			nil,
			nil,
		)

		if !known {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictBoolParam().Format(name),
				arg.D.ValueExpr,
				nil,
			)
			return
		}

		if name == "total" && !constArgValue {
			classType.Shared.Flags |= ClassTypeFlagsCanOmitDictValues
			return
		}

		if name != "closed" {
			return
		}

		if constArgValue {
			classType.Shared.Flags |= ClassTypeFlagsTypedDictMarkedClosed | ClassTypeFlagsTypedDictEffectivelyClosed

			if classType.Shared.TypedDictExtraItemsExpr != nil {
				e.AddDiagnostic(
					DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypedDictExtraItemsClosed(),
					classType.Shared.TypedDictExtraItemsExpr,
					nil,
				)
			}
		} else {
			// The original's comment: PEP 728: a class that subclasses from a
			// non-open TypedDict cannot specify closed=False.
			for _, base := range classType.Shared.BaseClasses {
				if IsInstantiableClass(base) &&
					ClassTypeIsTypedDictClass(base.(*ClassType)) &&
					ClassTypeIsTypedDictEffectivelyClosed(base.(*ClassType)) {
					e.AddDiagnostic(
						DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.TypedDictClosedFalseNonOpenBase().Format(base.(*ClassType).Shared.Name),
						arg.D.ValueExpr,
						nil,
					)
					break
				}
			}
		}

		if *sawClosedOrExtraItems {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictExtraItemsClosed(),
				arg.D.ValueExpr,
				nil,
			)
		}

		*sawClosedOrExtraItems = true

	case "extra_items":
		// The original's comment: record a reference to the expression but
		// don't evaluate it yet. It may refer to the class itself.
		classType.Shared.TypedDictExtraItemsExpr = arg.D.ValueExpr
		classType.Shared.Flags |= ClassTypeFlagsTypedDictEffectivelyClosed

		if ClassTypeIsTypedDictMarkedClosed(classType) {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictExtraItemsClosed(),
				classType.Shared.TypedDictExtraItemsExpr,
				nil,
			)
		}

		if *sawClosedOrExtraItems {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypedDictExtraItemsClosed(),
				arg.D.ValueExpr,
				nil,
			)
		}

		*sawClosedOrExtraItems = true

	default:
		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypedDictInitsubclassParameter().Format(name),
			arg,
			nil,
		)
	}
}

/*
 * The remaining stages, each the original's inline block.
 */

// checkClassVariadics is the original's TypeVarTuple checks.
func (e *typeEvaluator) checkClassVariadics(node *parser.ClassNode, typeParams []*TypeVarType) {
	// The original's comment: make sure there's at most one TypeVarTuple.
	var variadics []*TypeVarType
	for _, param := range typeParams {
		if IsTypeVarTuple(param) {
			variadics = append(variadics, param)
		}
	}

	if len(variadics) > 1 {
		names := make([]string, 0, len(variadics))
		for _, v := range variadics {
			names = append(names, fmt.Sprintf("%q", v.Shared.Name))
		}

		var textRange *common.TextRange
		if combined, ok := combineArgRanges(node.D.Arguments); ok {
			textRange = &combined
		}

		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.VariadicTypeParamTooManyClass().Format(strings.Join(names, ", ")),
			node.D.Name,
			textRange,
		)
		return
	}

	if len(variadics) == 0 {
		return
	}

	// The original's comment: make sure a TypeVar with a default doesn't come
	// after a TypeVarTuple.
	firstVariadicIndex := -1
	for i, param := range typeParams {
		if IsTypeVarTuple(param) {
			firstVariadicIndex = i
			break
		}
	}

	typeVarWithDefaultIndex := -1
	for i, param := range typeParams {
		if i > firstVariadicIndex && !IsParamSpec(param) && param.Shared.IsDefaultExplicit {
			typeVarWithDefaultIndex = i
			break
		}
	}

	if typeVarWithDefaultIndex >= 0 {
		var errorNode parser.ParseNode = node.D.Name
		if node.D.TypeParams != nil && typeVarWithDefaultIndex < len(node.D.TypeParams.D.Params) {
			errorNode = node.D.TypeParams.D.Params[typeVarWithDefaultIndex].D.Name
		}

		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarWithDefaultFollowsVariadic().Format(
				typeParams[firstVariadicIndex].Shared.Name,
				typeParams[typeVarWithDefaultIndex].Shared.Name,
			),
			errorNode,
			nil,
		)
	}
}

// applyPseudoGenericTypeParams is the original's pseudo-generic block.
func (e *typeEvaluator) applyPseudoGenericTypeParams(
	node *parser.ClassNode,
	classType *ClassType,
	fileInfo *AnalyzerFileInfo,
) {
	// The original's comment: determine if the class should be a
	// "pseudo-generic" class, characterized by having an __init__ method with
	// parameters that lack type annotations. For such classes, we'll treat them
	// as generic, with the type arguments provided by the callers of the
	// constructor.
	if fileInfo.IsStubFile || len(classType.Shared.TypeParams) > 0 {
		return
	}

	initMethod, ok := classType.Shared.Fields.Get("__init__")
	if !ok || initMethod == nil {
		return
	}

	initDecls := initMethod.GetTypedDeclarations()
	if len(initDecls) != 1 || initDecls[0].DeclBase().Type != DeclarationTypeFunction {
		return
	}

	initDeclNode, ok := initDecls[0].DeclBase().Node.(*parser.FunctionNode)
	if !ok {
		return
	}
	initParams := initDeclNode.D.Params

	if len(initParams) <= 1 {
		return
	}
	for index := range initParams {
		if GetTypeAnnotationForParam(initDeclNode, index) != nil {
			return
		}
	}

	var genericParams []*parser.ParameterNode
	for index, param := range initParams {
		if index > 0 && param.D.Name != nil &&
			param.D.Category == parser.ParamCategorySimple &&
			param.D.DefaultValue == nil {
			genericParams = append(genericParams, param)
		}
	}

	if len(genericParams) == 0 {
		return
	}

	classType.Shared.Flags |= ClassTypeFlagsPseudoGenericClass

	// The original's comment: create a type parameter for each simple, named
	// parameter in the __init__ method.
	newTypeParams := make([]*TypeVarType, 0, len(genericParams))
	for _, param := range genericParams {
		typeVar := TypeVarTypeCreateInstance(getPseudoGenericTypeVarName(param.D.Name.D.Value), TypeVarKindTypeVar)
		typeVar.Shared.IsSynthesized = true
		typeVar.Priv.ScopeID = GetScopeIdForNode(initDeclNode)
		typeVar.Shared.BoundType = UnknownTypeCreate(false)
		scopeName := node.D.Name.D.Value
		scopeType := TypeVarScopeTypeClass
		newTypeParams = append(newTypeParams, TypeVarTypeCloneForScopeID(
			typeVar,
			GetScopeIdForNode(node),
			&scopeName,
			&scopeType,
		))
	}
	classType.Shared.TypeParams = newTypeParams
}

// applyDeclaredMetaclass is the original's `if (metaclassNode)` block.
func (e *typeEvaluator) applyDeclaredMetaclass(
	classType *ClassType,
	metaclassNode parser.ExpressionNode,
	exprFlags EvalFlags,
) {
	if metaclassNode == nil {
		return
	}

	metaclassType := e.getTypeOfExpression(metaclassNode, exprFlags, nil).Type
	if !IsInstantiableClass(metaclassType) && !IsUnknown(metaclassType) {
		return
	}

	if RequiresSpecialization(metaclassType, &RequiresSpecializationOptions{IgnorePseudoGeneric: true}, 0) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.MetaclassIsGeneric(), metaclassNode, nil)
	}

	// The original's comment: if the specified metaclass is type(T), use the
	// metaclass of T if it's known.
	if metaclassType.Base().GetInstantiableDepth() > 0 && IsClass(metaclassType) {
		if meta := metaclassType.(*ClassType).Shared.EffectiveMetaclass; meta != nil && IsClass(meta) {
			metaclassType = meta
		}
	}

	classType.Shared.DeclaredMetaclass = metaclassType
	if IsInstantiableClass(metaclassType) {
		mc := metaclassType.(*ClassType)
		if IsEnumMetaclass(mc) {
			classType.Shared.Flags |= ClassTypeFlagsEnumClass
		}

		if DerivesFromStdlibClass(mc, "ABCMeta") {
			classType.Shared.Flags |= ClassTypeFlagsSupportsAbstractMethods
		}
	}
}

// applyEnumFlags is the original's two enum-flag blocks.
func (e *typeEvaluator) applyEnumFlags(node *parser.ClassNode, classType *ClassType, effectiveMetaclass Type) {
	if !ClassTypeIsEnumClass(classType) {
		return
	}

	enumMemberSetMayBeDynamicallyModified := false
	if scope := GetScope(node); scope != nil && scope.HasPotentiallyDynamicSymbolTable {
		enumMemberSetMayBeDynamicallyModified = true
	}
	if !enumMemberSetMayBeDynamicallyModified {
		for _, mroClass := range classType.Shared.Mro {
			if IsClass(mroClass) && ClassTypeIsEnumMemberSetMayBeDynamicallyModified(mroClass.(*ClassType)) {
				enumMemberSetMayBeDynamicallyModified = true
				break
			}
		}
	}

	if enumMemberSetMayBeDynamicallyModified {
		classType.Shared.Flags |= ClassTypeFlagsEnumMemberSetMayBeDynamicallyModified
	}

	mayBeIncomplete := len(node.D.Decorators) > 0 ||
		enumMemberSetMayBeDynamicallyModified ||
		!IsInstantiableClass(effectiveMetaclass) ||
		!ClassTypeIsBuiltInNamed(effectiveMetaclass.(*ClassType), "EnumMeta", "EnumType")

	if !mayBeIncomplete {
		for _, mroClass := range classType.Shared.Mro {
			if !IsClass(mroClass) {
				continue
			}
			mc := mroClass.(*ClassType)
			if ClassTypeIsEnumMemberSetMayBeIncomplete(mc) {
				mayBeIncomplete = true
				break
			}
			if !ClassTypeIsBuiltInNamed(mc, "Enum") {
				if _, ok := ClassTypeGetSymbolTable(mc).Get("_missing_"); ok {
					mayBeIncomplete = true
					break
				}
			}
		}
	}

	if mayBeIncomplete {
		classType.Shared.Flags |= ClassTypeFlagsEnumMemberSetMayBeIncomplete
	}
}

// applyClassDecorators is the original's decorator loop, which runs in reverse
// source order.
func (e *typeEvaluator) applyClassDecorators(node *parser.ClassNode, classType *ClassType) Type {
	var decoratedType Type = classType
	foundUnknown := false

	for i := len(node.D.Decorators) - 1; i >= 0; i-- {
		decorator := node.D.Decorators[i]

		var trackerNode parser.ParseNode = node.NodeBase().Parent
		if trackerNode == nil {
			trackerNode = node
		}

		captured := decoratedType
		var newDecoratedType Type
		e.withSignatureTracker(trackerNode, func() {
			newDecoratedType = e.applyClassDecorator(captured, classType, decorator)
		})

		unknownOrAny := ContainsAnyOrUnknown(newDecoratedType, false)

		if unknownOrAny != nil && IsUnknown(unknownOrAny) {
			// The original's comment: report this error only on the first
			// unknown type.
			if !foundUnknown {
				e.AddDiagnostic(
					DiagnosticRuleReportUntypedClassDecorator,
					localization.LocMessage.ClassDecoratorTypeUnknown(),
					node.D.Decorators[i].D.Expr,
					nil,
				)

				foundUnknown = true
			}
		} else {
			// The original's comment: apply the decorator only if the type is
			// known.
			decoratedType = newDecoratedType
		}
	}

	return decoratedType
}

// applyDataClassBehaviors is the original's dataclass-behaviors block.
func (e *typeEvaluator) applyDataClassBehaviors(
	node *parser.ClassNode,
	classType *ClassType,
	effectiveMetaclass Type,
	initSubclassArgs []*Arg,
) {
	// The original's comment: determine whether this class derives from (or has
	// a metaclass) that imbues it with dataclass-like behaviors. If so, we'll
	// apply those here.
	var dataClassBehaviors *DataClassBehaviors

	if IsInstantiableClass(effectiveMetaclass) && effectiveMetaclass.(*ClassType).Shared.ClassDataClassTransform != nil {
		dataClassBehaviors = effectiveMetaclass.(*ClassType).Shared.ClassDataClassTransform
	} else {
		for _, mroClass := range classType.Shared.Mro {
			if IsClass(mroClass) {
				mc := mroClass.(*ClassType)
				if mc.Shared.ClassDataClassTransform != nil && !ClassTypeIsSameGenericClass(mc, classType, 0) {
					dataClassBehaviors = mc.Shared.ClassDataClassTransform
					break
				}
			}
		}
	}

	if dataClassBehaviors != nil {
		e.applyDataClassClassBehaviorOverrides(node.D.Name, classType, initSubclassArgs, dataClassBehaviors)
	}
}

// synthesizeTypedDictIfNeeded is the original's TypedDict synthesis block.
func (e *typeEvaluator) synthesizeTypedDictIfNeeded(node *parser.ClassNode, classType *ClassType) {
	if !ClassTypeIsTypedDictClass(classType) {
		return
	}

	// The original's comment: TypedDict classes must derive only from other
	// TypedDict classes.
	foundInvalidBaseClass := false
	diag := common.NewDiagnosticAddendum()

	for _, baseClass := range classType.Shared.BaseClasses {
		if IsClass(baseClass) {
			bc := baseClass.(*ClassType)
			if !ClassTypeIsTypedDictClass(bc) &&
				!ClassTypeIsBuiltInNamed(bc, "_TypedDict", "TypedDictFallback", "Generic") {
				foundInvalidBaseClass = true
				diag.AddMessage(localization.LocAddendum.TypedDictBaseClass().Format(bc.Shared.Name))
			}
		}
	}

	if foundInvalidBaseClass {
		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypedDictBaseClass()+diag.GetString(),
			node.D.Name,
			nil,
		)
	}

	e.synthesizeTypedDictClassMethods(node, classType)
}

// synthesizeDataClassIfNeeded is the original's dataclass synthesis block.
func (e *typeEvaluator) synthesizeDataClassIfNeeded(
	node *parser.ClassNode,
	classType *ClassType,
	isNamedTupleSubclass bool,
) {
	if !ClassTypeIsDataClass(classType) && !isNamedTupleSubclass {
		return
	}

	skipSynthesizedInit := ClassTypeIsDataClassSkipGenerateInit(classType)
	hasExistingInitMethod := skipSynthesizedInit

	// The original's comment: see if there's already a non-synthesized __init__
	// method. We shouldn't override it.
	if !skipSynthesizedInit {
		if initSymbol, ok := classType.Shared.Fields.Get("__init__"); ok && initSymbol.IsClassMember() {
			hasExistingInitMethod = true
		}
	}

	skipSynthesizeHash := false
	// The original's comment: if there is a hash symbol defined in the class
	// (i.e. one that we didn't synthesize above), then we shouldn't synthesize a
	// new one for the dataclass.
	if hashSymbol, ok := classType.Shared.Fields.Get("__hash__"); ok &&
		hashSymbol.IsClassMember() && hashSymbol.GetSynthesizedType() == nil {
		skipSynthesizeHash = true
	}

	synthesizeMethods := func() {
		e.synthesizeDataClassMethods(
			node,
			classType,
			isNamedTupleSubclass,
			skipSynthesizedInit,
			hasExistingInitMethod,
			skipSynthesizeHash,
		)
	}

	// The original's comment: if this is a NamedTuple subclass, immediately
	// synthesize dataclass methods because we also need to update the MRO
	// classes in this case. For regular dataclasses, we'll defer the method
	// synthesis to avoid circular dependencies.
	if isNamedTupleSubclass {
		synthesizeMethods()
		return
	}

	if ClassTypeIsDataClassGenerateSlots(classType) {
		classType.Shared.SynthesizeDataClassSlotsDeferred = func() {
			e.synthesizeDataClassSlots(classType)
		}
	}

	classType.Shared.SynthesizeMethodsDeferred = func() {
		classType.Shared.SynthesizeMethodsDeferred = nil
		synthesizeMethods()
	}
}

// calculateInheritedSlotsNames is the body of the original's
// calculateInheritedSlotsNamesDeferred callback.
func (e *typeEvaluator) calculateInheritedSlotsNames(classType *ClassType) {
	if classType.Shared.LocalSlotsNames == nil {
		return
	}

	isLimitedToSlots := true
	extendedSlotsNames := append([]string(nil), classType.Shared.LocalSlotsNames...)

	for _, baseClass := range classType.Shared.BaseClasses {
		if !IsInstantiableClass(baseClass) {
			isLimitedToSlots = false
			continue
		}

		bc := baseClass.(*ClassType)
		if ClassTypeIsBuiltInNamed(bc, "object") ||
			ClassTypeIsBuiltInNamed(bc, "type") ||
			ClassTypeIsBuiltInNamed(bc, "Generic") {
			continue
		}

		if inheritedSlotsNames := ClassTypeGetInheritedSlotsNames(bc); inheritedSlotsNames != nil {
			extendedSlotsNames = append(extendedSlotsNames, inheritedSlotsNames...)
		} else {
			isLimitedToSlots = false
		}
	}

	if isLimitedToSlots {
		classType.Shared.InheritedSlotsNamesCached = extendedSlotsNames
	}
}

/*
 * The helpers that exist only to serve class creation.
 */

// buildTypeParamsFromTypeArgs corresponds to the function of the same name.
func buildTypeParamsFromTypeArgs(classType *ClassType) []*TypeVarType {
	typeParams := []*TypeVarType{}
	typeArgs := classType.Priv.TypeArgs

	for index, typeArg := range typeArgs {
		if IsTypeVar(typeArg) {
			typeParams = append(typeParams, typeArg.(*TypeVarType))
			continue
		}

		// The original's comment: synthesize a dummy type parameter.
		typeVar := TypeVarTypeCreateInstance(fmt.Sprintf("__P%d", index), TypeVarKindTypeVar)
		typeVar.Shared.IsSynthesized = true
		typeParams = append(typeParams, typeVar)
	}

	return typeParams
}

// validateTypeParamDefault corresponds to the function of the same name. The
// original's comment: determines whether the type parameters has a default that
// refers to another type parameter. If so, validates that it is in the list of
// "live" type parameters and updates the scope of the type parameter referred to
// in the default type expression.
func (e *typeEvaluator) validateTypeParamDefault(
	errorNode parser.ExpressionNode,
	typeParam *TypeVarType,
	otherLiveTypeParams []*TypeVarType,
	scopeID TypeVarScopeId,
) {
	if !typeParam.Shared.IsDefaultExplicit && !typeParam.Shared.IsSynthesized && !TypeVarTypeIsSelf(typeParam) {
		for _, param := range otherLiveTypeParams {
			if param.Shared.IsDefaultExplicit && param.Priv.ScopeID == scopeID {
				e.AddDiagnostic(
					DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.TypeVarWithoutDefault().Format(param.Shared.Name, typeParam.Shared.Name),
					errorNode,
					nil,
				)
				break
			}
		}
		return
	}

	invalidTypeVars := common.NewOrderedSet[string]()
	ValidateTypeVarDefault(typeParam, otherLiveTypeParams, invalidTypeVars)

	// The original's comment: if we found one or more unapplied type variable,
	// report an error.
	if invalidTypeVars.Size() > 0 {
		diag := common.NewDiagnosticAddendum()
		for _, name := range invalidTypeVars.Values() {
			diag.AddMessage(localization.LocAddendum.TypeVarDefaultOutOfScope().Format(name))
		}

		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarDefaultInvalidTypeVar().Format(typeParam.Shared.Name)+diag.GetString(),
			errorNode,
			nil,
		)
	}
}

// evaluateTypeParamList corresponds to the function of the same name.
func (e *typeEvaluator) evaluateTypeParamList(node *parser.TypeParameterListNode) []*TypeVarType {
	paramTypes := []*TypeVarType{}
	typeParamScope := GetScope(node)

	for _, param := range node.D.Params {
		if typeParamScope == nil || typeParamScope.SymbolTable == nil {
			continue
		}
		paramSymbol, ok := typeParamScope.SymbolTable.Get(param.D.Name.D.Value)
		if !ok || paramSymbol == nil {
			// The original's comment: this can happen if the code is
			// unreachable.
			continue
		}

		declaredTypeInfo := e.getDeclaredTypeOfSymbol(paramSymbol, param.D.Name)
		if declaredTypeInfo == nil || declaredTypeInfo.Type == nil || !IsTypeVar(declaredTypeInfo.Type) {
			continue
		}
		typeOfParam := declaredTypeInfo.Type.(*TypeVarType)

		e.writeTypeCache(param.D.Name, &TypeResult{Type: typeOfParam}, evalFlagsNonePtr(), nil, false)
		paramTypes = append(paramTypes, typeOfParam)
	}

	return paramTypes
}

// computeEffectiveMetaclass corresponds to the function of the same name.
func (e *typeEvaluator) computeEffectiveMetaclass(classType *ClassType, errorNode parser.ParseNode) Type {
	effectiveMetaclass := classType.Shared.DeclaredMetaclass
	reportedMetaclassConflict := false

	if effectiveMetaclass == nil || IsInstantiableClass(effectiveMetaclass) {
		for _, baseClass := range classType.Shared.BaseClasses {
			if !IsInstantiableClass(baseClass) {
				// The original's comment: if one of the base classes is
				// unknown, then the effective metaclass is also unknowable.
				effectiveMetaclass = UnknownTypeCreate(false)
				break
			}

			baseClassMeta := baseClass.(*ClassType).Shared.EffectiveMetaclass
			if baseClassMeta == nil && e.prefetched != nil {
				baseClassMeta = e.prefetched.TypeClass
			}

			if baseClassMeta == nil || !IsInstantiableClass(baseClassMeta) {
				if baseClassMeta != nil {
					effectiveMetaclass = UnknownTypeCreate(false)
				} else {
					effectiveMetaclass = nil
				}
				break
			}

			// The original's comment: make sure there is no metaclass conflict.
			if effectiveMetaclass == nil {
				effectiveMetaclass = baseClassMeta
			} else if DerivesFromClassRecursive(baseClassMeta.(*ClassType), effectiveMetaclass.(*ClassType), false) {
				effectiveMetaclass = baseClassMeta
			} else if !DerivesFromClassRecursive(effectiveMetaclass.(*ClassType), baseClassMeta.(*ClassType), false) {
				if !reportedMetaclassConflict {
					diag := common.NewDiagnosticAddendum()

					diag.AddMessage(localization.LocAddendum.MetaclassConflict().Format(
						e.PrintType(ConvertToInstance(effectiveMetaclass, false), nil),
						e.PrintType(ConvertToInstance(baseClassMeta, false), nil),
					))
					e.AddDiagnostic(
						DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.MetaclassConflict()+diag.GetString(),
						errorNode,
						nil,
					)

					// The original's comment: don't report more than once.
					reportedMetaclassConflict = true
				}
			}
		}
	}

	// The original's comment: if we haven't found an effective metaclass,
	// assume "type", which is the metaclass for "object".
	if effectiveMetaclass == nil {
		typeMetaclass := e.GetBuiltInType(errorNode, "type")
		if typeMetaclass != nil && IsInstantiableClass(typeMetaclass) {
			effectiveMetaclass = typeMetaclass
		} else {
			effectiveMetaclass = UnknownTypeCreate(false)
		}
	}

	classType.Shared.EffectiveMetaclass = effectiveMetaclass

	return effectiveMetaclass
}

// verifyGenericTypeParams corresponds to the function of the same name. The
// original's comment: verifies that the type variables provided outside of
// "Generic" or "Protocol" are also provided within the "Generic".
func (e *typeEvaluator) verifyGenericTypeParams(
	errorNode parser.ExpressionNode,
	typeVars []*TypeVarType,
	genericTypeVars []*TypeVarType,
) {
	var missingFromGeneric []*TypeVarType
	for _, typeVar := range typeVars {
		found := false
		for _, genericTypeVar := range genericTypeVars {
			if genericTypeVar.Shared.Name == typeVar.Shared.Name {
				found = true
				break
			}
		}
		if !found {
			missingFromGeneric = append(missingFromGeneric, typeVar)
		}
	}

	if len(missingFromGeneric) > 0 {
		names := make([]string, 0, len(missingFromGeneric))
		for _, typeVar := range missingFromGeneric {
			names = append(names, fmt.Sprintf("%q", typeVar.Shared.Name))
		}

		diag := common.NewDiagnosticAddendum()
		diag.AddMessage(localization.LocAddendum.TypeVarsMissing().Format(strings.Join(names, ", ")))

		e.AddDiagnostic(
			DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.TypeVarsNotInGenericOrProtocol()+diag.GetString(),
			errorNode,
			nil,
		)
	}
}

// registerDeferredClassCompletion corresponds to the function of the same name.
// The original's comment: records the fact that the specified class requires
// "deferred completion" because one of its base classes has not yet been fully
// evaluated. If the caller passes undefined for "dependsUpon", then the class is
// added to all outstanding deferred completions.
func (e *typeEvaluator) registerDeferredClassCompletion(classToComplete *parser.ClassNode, dependsUpon *ClassType) {
	if dependsUpon == nil {
		for _, entry := range e.deferredClassCompletions {
			entry.ClassesToComplete = append(entry.ClassesToComplete, classToComplete)
		}
		return
	}

	// The original's comment: see if there is an existing entry for this
	// dependency.
	for _, entry := range e.deferredClassCompletions {
		if ClassTypeIsSameGenericClass(entry.DependsUpon, dependsUpon, 0) {
			entry.ClassesToComplete = append(entry.ClassesToComplete, classToComplete)
			return
		}
	}

	e.deferredClassCompletions = append(e.deferredClassCompletions, &DeferredClassCompletion{
		DependsUpon:       dependsUpon,
		ClassesToComplete: []*parser.ClassNode{classToComplete},
	})
}

// runDeferredClassCompletions corresponds to the function of the same name.
func (e *typeEvaluator) runDeferredClassCompletions(t *ClassType) {
	for _, entry := range e.deferredClassCompletions {
		if !ClassTypeIsSameGenericClass(entry.DependsUpon, t, 0) {
			continue
		}
		for _, classNode := range entry.ClassesToComplete {
			if classType := e.readTypeCache(classNode.D.Name, evalFlagsNonePtr()); classType != nil {
				if asClass, ok := classType.(*ClassType); ok {
					e.completeClassTypeDeferred(asClass, classNode.D.Name)
				}
			}
		}
	}

	// The original's comment: remove any completions that depend on this type.
	remaining := e.deferredClassCompletions[:0]
	for _, entry := range e.deferredClassCompletions {
		if !ClassTypeIsSameGenericClass(entry.DependsUpon, t, 0) {
			remaining = append(remaining, entry)
		}
	}
	e.deferredClassCompletions = remaining
}

// completeClassTypeDeferred corresponds to the function of the same name. The
// original's comment: recomputes the MRO and effective metaclass for the class
// after dependent classes have been fully constructed.
func (e *typeEvaluator) completeClassTypeDeferred(t *ClassType, errorNode parser.ParseNode) {
	// The original's comment: recompute the MRO linearization.
	if !ComputeMroLinearization(t) {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, localization.LocMessage.MethodOrdering(), errorNode, nil)
	}

	// The original's comment: recompute the effective metaclass.
	e.computeEffectiveMetaclass(t, errorNode)
}

// getPseudoGenericTypeVarName corresponds to the function of the same name.
func getPseudoGenericTypeVarName(paramName string) string {
	return "__type_of_" + paramName
}

// invalidateTypeCacheIfCanceled corresponds to the cancellationUtils function of
// the same name. The original catches an OperationCanceledException and sets
// isTypeCacheInvalid on it, so the caller knows the type cache holds a
// partially-constructed class. Cancellation is not carried by this port, so
// there is no exception to mark; the wrapper is kept so the place that flag
// would be set is visible rather than silently absent.
func (e *typeEvaluator) invalidateTypeCacheIfCanceled(cb func() *ClassTypeResult) *ClassTypeResult {
	return cb()
}

// evalFlagsNonePtr is the address of EvalFlagsNone, for the cache calls whose
// flags parameter is a pointer because the original distinguishes an absent
// flag set from EvalFlags.None.
func evalFlagsNonePtr() *EvalFlags {
	flags := EvalFlagsNone
	return &flags
}

// combineArgRanges corresponds to TextRange.combine over the class arguments.
func combineArgRanges(args []*parser.ArgumentNode) (common.TextRange, bool) {
	if len(args) == 0 {
		return common.TextRange{}, false
	}

	ranges := make([]common.TextRange, 0, len(args))
	for _, arg := range args {
		ranges = append(ranges, arg.NodeBase().TextRange)
	}
	combined := common.CombineTextRanges(ranges)
	if combined == nil {
		return common.TextRange{}, false
	}
	return *combined, true
}

/*
 * The satellites. Each is a separate module of the original and records itself
 * so the frontier ranks them.
 */

// applyClassDecorator is the evaluator-side wrapper over the decorators.ts
// function of the same name, which takes the evaluator as its first argument.
func (e *typeEvaluator) applyClassDecorator(
	inputClassType Type,
	originalClassType *ClassType,
	decoratorNode *parser.DecoratorNode,
) Type {
	return ApplyClassDecorator(e, inputClassType, originalClassType, decoratorNode)
}

// synthesizeTypedDictClassMethods corresponds to the typedDicts.ts function of
// the same name.
func (e *typeEvaluator) synthesizeTypedDictClassMethods(node *parser.ClassNode, classType *ClassType) {
	SynthesizeTypedDictClassMethods(e, node, classType)
}

// synthesizeDataClassMethods corresponds to the dataClasses.ts function of the
// same name.
func (e *typeEvaluator) synthesizeDataClassMethods(
	node *parser.ClassNode,
	classType *ClassType,
	isNamedTuple bool,
	skipSynthesizeInit bool,
	hasExistingInitMethod bool,
	skipSynthesizeHash bool,
) {
	SynthesizeDataClassMethods(
		e, node, classType, isNamedTuple, skipSynthesizeInit, hasExistingInitMethod, skipSynthesizeHash)
}

// synthesizeDataClassSlots corresponds to the dataClasses.ts function of the
// same name.
func (e *typeEvaluator) synthesizeDataClassSlots(classType *ClassType) {
	SynthesizeDataClassSlots(e, classType)
}

// applyDataClassClassBehaviorOverrides reaches the dataClasses.ts function of
// the same name.
func (e *typeEvaluator) applyDataClassClassBehaviorOverrides(
	errorNode *parser.NameNode,
	classType *ClassType,
	args []*Arg,
	defaultBehaviors *DataClassBehaviors,
) {
	ApplyDataClassClassBehaviorOverrides(e, errorNode, classType, args, defaultBehaviors)
}
