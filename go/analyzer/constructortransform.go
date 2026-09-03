/*
 * constructortransform.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/constructorTransform.ts (pyright 1.1.412):
 * applyConstructorTransform, applyPartialTransform and
 * applyPartialTransformToFunction.
 *
 * This exists for one class: `functools.partial`. Its type as written in
 * typeshed cannot express what it does -- the whole point is that
 * `partial(f, 1)` produces something callable with *f's remaining parameters*,
 * which depends on both f's signature and which arguments were supplied. So the
 * result of the constructor call is rewritten here with a `__call__` synthesized
 * for this particular partial application.
 *
 * The bound arguments are checked as they are consumed, against the parameters
 * they map to, exactly as a real call would be. That is what makes
 * `partial(f, "x")` an error at the partial() call rather than at the eventual
 * invocation.
 *
 * The resulting parameter list is reordered, and the order is deliberate:
 * unassigned parameters first, then keyword parameters that were bound, then
 * `**kwargs`. A bound keyword parameter is kept rather than removed -- it stays
 * callable, since Python allows overriding a partial's keyword binding -- but it
 * is given a default so it need not be supplied again, and it is moved after the
 * unassigned ones so it does not occupy a positional slot.
 *
 * An overloaded original produces an overloaded `__call__` containing only the
 * overloads the bound arguments are compatible with, which is why each is
 * transformed with a nil errorNode: the failures are expected and only the
 * survivors matter. If none survive, the usual no-overload diagnostic is
 * reported once against the call.
 *
 * The original threads a FunctionResult through applyConstructorTransform; the
 * Go port already uses CallResult at this seam and the two carry the same three
 * fields, so that choice is kept rather than churning the caller. The inner
 * per-signature helper still returns FunctionResult, matching the original.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// ApplyConstructorTransform corresponds to applyConstructorTransform.
func ApplyConstructorTransform(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	classType *ClassType,
	result *CallResult,
) *CallResult {
	if classType.Shared.FullName == "functools.partial" {
		return applyPartialTransform(evaluator, errorNode, argList, result)
	}

	// The original's comment: by default, return the result unmodified.
	return result
}

// applyPartialTransform corresponds to the function of the same name. The
// original's comment: applies a transform for the functools.partial class
// constructor.
func applyPartialTransform(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	result *CallResult,
) *CallResult {
	// The original's comment: we assume that the normal return result is a
	// functools.partial class instance.
	if !IsClassInstance(result.ReturnType) ||
		result.ReturnType.(*ClassType).Shared.FullName != "functools.partial" {
		return nil
	}
	partialInstance := result.ReturnType.(*ClassType)

	callMemberResult := LookUpObjectMember(partialInstance, "__call__",
		MemberAccessFlagsSkipInstanceMembers, nil)
	if callMemberResult == nil {
		return nil
	}
	// The `__call__` must come from functools.partial itself; a subclass that
	// overrides it is not something this transform can rewrite.
	if !IsTypeSame(ConvertToInstance(callMemberResult.ClassType, true), partialInstance,
		TypeSameOptions{}, 0) {
		return nil
	}

	callMemberType := evaluator.GetTypeOfMember(callMemberResult)
	if !IsFunction(callMemberType) ||
		len(callMemberType.(*FunctionType).Shared.Parameters) < 1 {
		return nil
	}
	partialCallMemberType := callMemberType.(*FunctionType)

	if len(argList) < 1 {
		return nil
	}

	origFunctionTypeResult := evaluator.GetTypeOfArg(argList[0], nil)
	origFunctionType := origFunctionTypeResult.Type
	origFunctionTypeConcrete := evaluator.MakeTopLevelTypeVarsConcrete(origFunctionType, false)

	if IsInstantiableClass(origFunctionTypeConcrete) {
		var selfType Type
		if IsTypeVar(origFunctionType) {
			selfType = ConvertToInstance(origFunctionType, true)
		}

		if constructor := CreateFunctionFromConstructor(evaluator,
			origFunctionTypeConcrete.(*ClassType), selfType, 0); constructor != nil {
			origFunctionType = constructor
		}
	}

	// The original's comment: evaluate the inferred return type if necessary.
	evaluator.InferReturnTypeIfNecessary(origFunctionType)

	// The original's comment: we don't currently handle unpacked arguments.
	for _, arg := range argList {
		if arg.ArgCategory != parser.ArgCategorySimple {
			return nil
		}
	}

	// The original's comment: make sure the first argument is a simple function.
	if IsFunction(origFunctionType) {
		transformResult := applyPartialTransformToFunction(evaluator, errorNode, argList,
			partialCallMemberType, origFunctionType.(*FunctionType))
		if transformResult == nil {
			return nil
		}

		// The original's comment: create a new copy of the functools.partial class
		// that overrides the __call__ method.
		newPartialClass := ClassTypeCloneForPartial(partialInstance, transformResult.ReturnType)

		return &CallResult{
			ReturnType:       newPartialClass,
			IsTypeIncomplete: result.IsTypeIncomplete,
			ArgumentErrors:   transformResult.ArgumentErrors,
		}
	}

	if !IsOverloaded(origFunctionType) {
		return nil
	}

	applicableOverloads := []*FunctionType{}
	overloads := OverloadedTypeGetOverloads(origFunctionType.(*OverloadedType))
	sawArgErrors := false

	// The original's comment: apply the partial transform to each of the functions
	// in the overload. The nil errorNode is what suppresses the expected failures.
	for _, overload := range overloads {
		transformResult := applyPartialTransformToFunction(evaluator, nil, argList,
			partialCallMemberType, overload)

		if transformResult == nil {
			continue
		}
		if transformResult.ArgumentErrors {
			sawArgErrors = true
		} else if IsFunction(transformResult.ReturnType) {
			applicableOverloads = append(applicableOverloads, transformResult.ReturnType.(*FunctionType))
		}
	}

	if len(applicableOverloads) == 0 {
		if sawArgErrors && len(overloads) > 0 {
			evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.NoOverload().Format(overloads[0].Shared.Name),
				errorNode, nil)
		}

		return nil
	}

	var synthesizedCallType Type
	if len(applicableOverloads) == 1 {
		synthesizedCallType = applicableOverloads[0]
	} else {
		// The original's comment: set the "overloaded" flag for each of the
		// __call__ overloads.
		flagged := make([]*FunctionType, 0, len(applicableOverloads))
		for _, overload := range applicableOverloads {
			flagged = append(flagged, FunctionTypeCloneWithNewFlags(overload,
				overload.Shared.Flags|FunctionTypeFlagsOverloaded))
		}
		synthesizedCallType = OverloadedTypeCreate(flagged, nil)
	}

	// The original's comment: create a new copy of the functools.partial class
	// that overrides the __call__ method.
	newPartialClass := ClassTypeCloneForPartial(partialInstance, synthesizedCallType)

	return &CallResult{
		ReturnType:       newPartialClass,
		IsTypeIncomplete: result.IsTypeIncomplete,
		ArgumentErrors:   false,
	}
}

// partialParamMap tracks which parameters the partial call supplied. The value
// distinguishes *how*: true for a keyword binding, which stays callable and gains
// a default, and false for a positional one, which is consumed outright.
type partialParamMap struct {
	assignedByKeyword map[string]bool
}

func newPartialParamMap() *partialParamMap {
	return &partialParamMap{assignedByKeyword: map[string]bool{}}
}

func (m *partialParamMap) Set(name string, byKeyword bool) { m.assignedByKeyword[name] = byKeyword }
func (m *partialParamMap) Has(name string) bool            { _, ok := m.assignedByKeyword[name]; return ok }
func (m *partialParamMap) Get(name string) bool            { return m.assignedByKeyword[name] }

// applyPartialTransformToFunction corresponds to the function of the same name.
func applyPartialTransformToFunction(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	partialCallMemberType *FunctionType,
	origFunctionType *FunctionType,
) *FunctionResult {
	// The original's comment: create a map to track which parameters have supplied
	// arguments.
	paramMap := newPartialParamMap()

	paramListDetails := GetParamListDetails(origFunctionType, nil)

	// The original's comment: verify the types of the provided arguments.
	state := &partialTransformState{
		evaluator:        evaluator,
		errorNode:        errorNode,
		origFunctionType: origFunctionType,
		paramListDetails: paramListDetails,
		paramMap:         paramMap,
		constraints:      NewConstraintTracker(),
	}

	for argIndex, arg := range argList[1:] {
		if arg.ValueExpression == nil {
			continue
		}

		// The original's comment: is it a positional argument or a keyword
		// argument?
		if arg.Name == nil {
			state.bindPositionalArg(arg, argIndex)
		} else {
			state.bindKeywordArg(arg)
		}
	}

	specializedFunctionType := evaluator.SolveAndApplyConstraints(
		origFunctionType, state.constraints, nil, nil)
	if !IsFunction(specializedFunctionType) {
		return nil
	}
	specializedFn := specializedFunctionType.(*FunctionType)

	newParamList := buildPartialParamList(evaluator, specializedFn, paramMap)

	// The original's comment: create a new __call__ method that uses the remaining
	// parameters.
	newCallMemberType := FunctionTypeCreateInstance(
		partialCallMemberType.Shared.Name,
		partialCallMemberType.Shared.FullName,
		partialCallMemberType.Shared.ModuleName,
		partialCallMemberType.Shared.Flags,
		specializedFn.Shared.DocString,
	)

	if len(partialCallMemberType.Shared.Parameters) > 0 {
		FunctionTypeAddParam(newCallMemberType, partialCallMemberType.Shared.Parameters[0])
	}
	for _, param := range newParamList {
		FunctionTypeAddParam(newCallMemberType, param)
	}

	if specializedFn.Shared.DeclaredReturnType != nil {
		newCallMemberType.Shared.DeclaredReturnType =
			FunctionTypeGetEffectiveReturnType(specializedFn, false)
	} else if specializedFn.Shared.InferredReturnType != nil {
		newCallMemberType.Shared.DeclaredReturnType = specializedFn.Shared.InferredReturnType.Type
	}
	newCallMemberType.Shared.Declaration = partialCallMemberType.Shared.Declaration
	newCallMemberType.Shared.TypeVarScopeID = specializedFn.Shared.TypeVarScopeID

	return &FunctionResult{
		ReturnType:       newCallMemberType,
		IsTypeIncomplete: false,
		ArgumentErrors:   state.argumentErrors,
	}
}

// partialTransformState carries what the original's forEach closes over.
type partialTransformState struct {
	evaluator               TypeEvaluator
	errorNode               parser.ExpressionNode
	origFunctionType        *FunctionType
	paramListDetails        *ParamListDetails
	paramMap                *partialParamMap
	constraints             *ConstraintTracker
	argumentErrors          bool
	reportedPositionalError bool
}

// checkArgAgainstParam evaluates an argument in the context of a parameter type
// and reports a mismatch. Every argument path in the original does this.
func (s *partialTransformState) checkArgAgainstParam(
	arg *Arg, paramType Type, paramName string,
) {
	diag := common.NewDiagnosticAddendum()

	argTypeResult := s.evaluator.GetTypeOfExpression(arg.ValueExpression, EvalFlagsNone,
		MakeInferenceContext(paramType, false, nil))

	if s.evaluator.AssignType(paramType, argTypeResult.Type, diag, s.constraints,
		AssignTypeFlagsDefault, 0) {
		return
	}

	if s.errorNode != nil {
		node := arg.ValueExpression
		if node == nil {
			node = s.errorNode
		}
		s.evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType,
			localization.LocMessage.ArgAssignmentParamFunction().Format(
				s.evaluator.PrintType(argTypeResult.Type, nil),
				s.evaluator.PrintType(paramType, nil),
				s.origFunctionType.Shared.Name,
				paramName),
			node, nil)
	}

	s.argumentErrors = true
}

// bindPositionalArg is the original's `if (!arg.name)` arm.
func (s *partialTransformState) bindPositionalArg(arg *Arg, argIndex int) {
	details := s.paramListDetails

	// The original's comment: does this positional argument map to a positional
	// parameter?
	mapsToPositional := argIndex < len(details.Params) &&
		details.Params[argIndex].Kind != ParamKindKeyword

	if mapsToPositional {
		paramInfo := details.Params[argIndex]
		paramName := ""
		if paramInfo.Param.Name != nil {
			paramName = *paramInfo.Param.Name
		}

		s.checkArgAgainstParam(arg, paramInfo.Type, paramName)

		// The original's comment: mark the parameter as assigned. False here means
		// "consumed positionally", which removes it from the resulting signature.
		s.paramMap.Set(paramName, false)
		return
	}

	// Extra positionals are legal only when the original has *args.
	if details.ArgsIndex != nil {
		paramType := FunctionTypeGetParamType(s.origFunctionType,
			details.Params[*details.ArgsIndex].Index)
		paramName := ""
		if details.Params[*details.ArgsIndex].Param.Name != nil {
			paramName = *details.Params[*details.ArgsIndex].Param.Name
		}

		s.checkArgAgainstParam(arg, paramType, paramName)
		return
	}

	// The original's comment: don't report multiple positional errors.
	if !s.reportedPositionalError && s.errorNode != nil {
		node := arg.ValueExpression
		if node == nil {
			node = s.errorNode
		}

		message := localization.LocMessage.ArgPositionalExpectedCount().
			Format(details.PositionParamCount)
		if details.PositionParamCount == 1 {
			message = localization.LocMessage.ArgPositionalExpectedOne()
		}

		s.evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue, message, node, nil)
	}

	s.reportedPositionalError = true
	s.argumentErrors = true
}

// bindKeywordArg is the original's `else` arm.
func (s *partialTransformState) bindKeywordArg(arg *Arg) {
	details := s.paramListDetails

	var matchingParam *VirtualParamDetails
	for _, paramInfo := range details.Params {
		if paramInfo.Param.Name != nil && *paramInfo.Param.Name == arg.Name.D.Value &&
			paramInfo.Kind != ParamKindPositional {
			matchingParam = paramInfo
			break
		}
	}

	if matchingParam == nil {
		// The original's comment: is there a kwargs parameter?
		if details.KwargsIndex == nil {
			if s.errorNode != nil {
				s.evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
					localization.LocMessage.ParamNameMissing().Format(arg.Name.D.Value),
					arg.Name, nil)
			}
			s.argumentErrors = true
			return
		}

		paramType := FunctionTypeGetParamType(s.origFunctionType,
			details.Params[*details.KwargsIndex].Index)
		paramName := ""
		if details.Params[*details.KwargsIndex].Param.Name != nil {
			paramName = *details.Params[*details.KwargsIndex].Param.Name
		}

		s.checkArgAgainstParam(arg, paramType, paramName)
		return
	}

	paramName := *matchingParam.Param.Name

	if s.paramMap.Has(paramName) {
		if s.errorNode != nil {
			s.evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.ParamAlreadyAssigned().Format(arg.Name.D.Value),
				arg.Name, nil)
		}
		s.argumentErrors = true
		return
	}

	s.checkArgAgainstParam(arg, matchingParam.Type, paramName)
	// True here means "bound by keyword": the parameter stays in the signature
	// but gains a default, since Python allows overriding it at the call.
	s.paramMap.Set(paramName, true)
}

// buildPartialParamList is the original's parameter-list reconstruction. See the
// file header for why the three groups are concatenated in this order.
func buildPartialParamList(
	evaluator TypeEvaluator, specializedFn *FunctionType, paramMap *partialParamMap,
) []FunctionParam {
	// The original's comment: create a new parameter list that omits parameters
	// that have been populated already.
	updatedParamList := make([]FunctionParam, 0, len(specializedFn.Shared.Parameters))

	for index, param := range specializedFn.Shared.Parameters {
		newType := FunctionTypeGetParamType(specializedFn, index)

		// The original's comment: if this is an **kwargs with an unpacked
		// TypedDict, mark the provided TypedDict entries as provided.
		if param.Category == parser.ParamCategoryKwargsDict && IsClassInstance(newType) &&
			IsUnpackedClass(newType) && ClassTypeIsTypedDictClass(newType.(*ClassType)) {
			tdClass := newType.(*ClassType)
			typedDictEntries := GetTypedDictMembersForClass(evaluator, tdClass, false)
			narrowedEntriesMap := common.NewOrderedMap[string, *TypedDictEntry]()
			if existing := tdClass.Priv.TypedDictNarrowedEntries; existing != nil {
				existing.ForEach(func(v *TypedDictEntry, k string) {
					narrowedEntriesMap.Set(k, v)
				})
			}

			typedDictEntries.KnownItems.ForEach(func(entry *TypedDictEntry, name string) {
				if paramMap.Has(name) {
					updated := *entry
					updated.IsRequired = false
					narrowedEntriesMap.Set(name, &updated)
				}
			})

			newType = ClassTypeCloneAsInstance(
				ClassTypeCloneForNarrowedTypedDictEntries(tdClass, narrowedEntriesMap), false)
		}

		// The original's comment: if it's a keyword parameter that has been
		// assigned a value through the "partial" mechanism, mark it has having a
		// default value.
		newDefaultType := FunctionTypeGetParamDefaultType(specializedFn, index)
		if param.Name != nil && paramMap.Get(*param.Name) {
			newDefaultType = AnyTypeCreate(true)
		}

		updatedParamList = append(updatedParamList, FunctionParamCreate(
			param.Category, newType, param.Flags, param.Name, newDefaultType, nil))
	}

	unassignedParamList := []FunctionParam{}
	assignedKeywordParamList := []FunctionParam{}
	kwargsParam := []FunctionParam{}

	for _, param := range updatedParamList {
		switch {
		case param.Category == parser.ParamCategoryKwargsDict:
			kwargsParam = append(kwargsParam, param)
		case param.Category == parser.ParamCategoryArgsList:
			unassignedParamList = append(unassignedParamList, param)
		case param.Name == nil || !paramMap.Has(*param.Name):
			unassignedParamList = append(unassignedParamList, param)
		}

		if param.Name != nil && paramMap.Get(*param.Name) {
			assignedKeywordParamList = append(assignedKeywordParamList, param)
		}
	}

	newParamList := make([]FunctionParam, 0,
		len(unassignedParamList)+len(assignedKeywordParamList)+len(kwargsParam))
	newParamList = append(newParamList, unassignedParamList...)
	newParamList = append(newParamList, assignedKeywordParamList...)
	newParamList = append(newParamList, kwargsParam...)

	return newParamList
}
