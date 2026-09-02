/*
 * typeevaluator_matchargs.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * expandArgList, matchArgsToParams.
 *
 * Deciding which argument goes to which parameter. Type checking is not done
 * here at all -- that is validateArgTypes' job; this is purely the positional
 * and keyword bookkeeping of PEP 3102, and it is the largest single function in
 * the evaluator because Python's calling convention has a great many shapes.
 *
 * The walk is in three phases, matching the language: positional arguments up to
 * the first keyword, then keyword arguments, then a check that every parameter
 * needing a value received one.
 *
 * Three things make it hard.
 *
 * UNPACKING. `f(*args)` may contribute any number of positional arguments, and
 * how many is often unknowable. expandArgList runs first and splits a `*t` whose
 * type is a fixed-length tuple into individual arguments, which is what makes
 * `f(*(1, "x"))` check properly. What remains is of unknown length, and the code
 * then has to be careful never to report "too many arguments" for something that
 * might contribute zero.
 *
 * PARAM SPECS. A function whose signature came from a ParamSpec captures its
 * arguments rather than matching them: they are collected into paramSpecArgList
 * and validated later against whatever the ParamSpec was solved to. PEP 612 also
 * requires that everything before `*args: P.args` be treated as positional-only,
 * which is why positionalOnlyLimitIndex moves.
 *
 * TYPEVARTUPLES. Arguments matched to a `*args: *Ts` parameter are not checked
 * one at a time; they are gathered back up into a synthesized tuple and matched
 * as a unit, at the end.
 *
 * The keyword phase has its own special case: `**kwargs` whose type is a
 * TypedDict is not opaque, so each of its known keys is matched to a parameter
 * by name, and the usual "argument missing" check is suppressed only for the
 * keys it actually supplies.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// MatchArgsToParamsResult corresponds to the interface of the same name.
type MatchArgsToParamsResult struct {
	Overload      *FunctionType
	OverloadIndex int

	ArgumentErrors   bool
	IsTypeIncomplete bool
	ArgParams        []*ValidateArgTypeParams
	ActiveParam      *FunctionParam
	ParamSpecTarget  *TypeVarType
	ParamSpecArgList []*Arg

	// UnpackedArgOfUnknownLength records whether there was an unpacked argument
	// of unknown length.
	UnpackedArgOfUnknownLength bool

	// UnpackedArgMapsToVariadic records whether that unpacked argument mapped to
	// a variadic parameter.
	UnpackedArgMapsToVariadic bool

	// ArgumentMatchScore indicates how well the overload matches the supplied
	// arguments, for picking the "best" one when reporting errors. Higher is
	// worse.
	ArgumentMatchScore int
}

// ValidateArgTypeOptions corresponds to the interface of the same name.
type ValidateArgTypeOptions struct {
	SkipUnknownArgCheck bool
	IsArgFirstPass      bool
	ConditionFilter     []TypeCondition
	SkipReportError     bool
}

// ParamSpecArgResult corresponds to the interface of the same name.
type ParamSpecArgResult struct {
	ArgumentErrors     bool
	ConstraintTrackers []*ConstraintTracker
}

// expandArgList corresponds to the function of the same name.
//
// Its comment at the call site: expand any unpacked tuples in the arg list.
// A `*t` whose type is a fixed-length tuple becomes individual arguments, which
// is what lets `f(*(1, "x"))` be checked position by position.
func (e *typeEvaluator) expandArgList(argList []*Arg) []*Arg {
	expandedArgList := []*Arg{}

	for _, arg := range argList {
		if arg.ArgCategory != parser.ArgCategoryUnpackedList {
			expandedArgList = append(expandedArgList, arg)
			continue
		}

		argType := e.GetTypeOfArg(arg, nil).Type

		// The original's comment: if this is a tuple with specified element types,
		// use those specified types rather than using the more generic iterator type
		// which will be a union of all element types.
		var tupleClass Type
		if e.prefetched != nil {
			tupleClass = e.prefetched.TupleClass
		}
		combinedArgType := CombineSameSizedTuples(e.MakeTopLevelTypeVarsConcrete(argType, false), tupleClass)

		combinedClass, isClass := combinedArgType.(*ClassType)
		if !isClass || !IsClassInstance(combinedArgType) || !IsTupleClass(combinedClass) {
			expandedArgList = append(expandedArgList, arg)
			continue
		}

		tupleTypeArgs := combinedClass.Priv.TupleTypeArgs

		if len(tupleTypeArgs) == 1 && tupleTypeArgs[0].IsUnbounded {
			expandedArgList = append(expandedArgList, arg)
			continue
		}

		for _, tupleTypeArg := range tupleTypeArgs {
			expanded := *arg
			expanded.ValueExpression = nil

			if tupleTypeArg.IsUnbounded {
				expanded.ArgCategory = parser.ArgCategoryUnpackedList
				expanded.TypeResult = &TypeResult{
					Type: MakeTupleObject(e, []*TupleTypeArg{tupleTypeArg}, false),
				}
			} else {
				expanded.ArgCategory = parser.ArgCategorySimple
				expanded.TypeResult = &TypeResult{Type: tupleTypeArg.Type}
			}

			expandedArgList = append(expandedArgList, &expanded)
		}
	}

	return expandedArgList
}

// argMatcher holds the state the original keeps in closures over the body of
// matchArgsToParams. Go needs it named because the phases became methods.
type argMatcher struct {
	e         *typeEvaluator
	errorNode parser.ExpressionNode
	argList   []*Arg
	overload  *FunctionType

	paramDetails *ParamListDetails
	paramSpec    *TypeVarType
	paramTracker *ParamAssignmentTracker

	argIndex   int
	paramIndex int

	positionalArgCount       int
	positionalOnlyLimitIndex int
	positionParamLimitIndex  int

	unpackedArgOfUnknownLength bool
	unpackedArgMapsToVariadic  bool
	reportedArgError           bool
	isTypeIncomplete           bool
	isTypeVarTupleFullyMatched bool

	paramSpecArgList       []*Arg
	paramSpecTarget        *TypeVarType
	hasParamSpecArgsKwargs bool

	argParams   []*ValidateArgTypeParams
	activeParam *FunctionParam
}

// trySetActive corresponds to the closure of the same name.
func (m *argMatcher) trySetActive(arg *Arg, param FunctionParam) {
	if arg.Active {
		copied := param
		m.activeParam = &copied
	}
}

// canReport is the original's repeated
// `!canSkipDiagnosticForNode(errorNode) && !isTypeIncomplete` guard.
func (m *argMatcher) canReport() bool {
	return !m.e.canSkipDiagnosticForNode(m.errorNode) && !m.isTypeIncomplete
}

// matchArgsToParams corresponds to the function of the same name.
//
// Its comment: matches the arguments passed to a function to the corresponding
// parameters in that function. This matching is done based on positions and
// keywords. Type evaluation and validation is left to the caller. This logic is
// based on PEP 3102: https://www.python.org/dev/peps/pep-3102/
func (e *typeEvaluator) matchArgsToParams(
	errorNode parser.ExpressionNode,
	argList []*Arg,
	typeResult *TypeResult,
	overloadIndex int,
) *MatchArgsToParamsResult {
	overload := typeResult.Type.(*FunctionType)
	disallow := true
	paramDetails := GetParamListDetails(overload, &ParamListDetailsOptions{DisallowExtraKwargsForTd: disallow})

	m := &argMatcher{
		e:                e,
		errorNode:        errorNode,
		overload:         overload,
		paramDetails:     paramDetails,
		paramSpec:        FunctionTypeGetParamSpecFromArgsKwargs(overload),
		isTypeIncomplete: typeResult.IsIncomplete,
		argParams:        []*ValidateArgTypeParams{},
	}

	// The original's comment: expand any unpacked tuples in the arg list.
	m.argList = e.expandArgList(argList)

	// The original's comment: construct an object that racks which parameters have
	// been assigned arguments.
	m.paramTracker = NewParamAssignmentTracker(paramDetails.Params)

	m.positionalOnlyLimitIndex = paramDetails.PositionOnlyParamCount
	m.positionParamLimitIndex = len(paramDetails.Params)
	if paramDetails.FirstKeywordOnlyIndex != nil {
		m.positionParamLimitIndex = *paramDetails.FirstKeywordOnlyIndex
	}

	m.computePositionalArgCount()
	m.detectParamSpec()
	m.limitPositionalsForKeywordArgs()

	// The original's comment: if we didn't see any special cases, then all
	// parameters are positional.
	if m.positionParamLimitIndex < 0 {
		m.positionParamLimitIndex = len(paramDetails.Params)
	}

	foundUnpackedListArg := false
	for _, arg := range m.argList {
		if arg.ArgCategory == parser.ArgCategoryUnpackedList {
			foundUnpackedListArg = true
			break
		}
	}

	m.matchPositionalArgs()
	skippedArgsParam := m.skipUnboundedPositionalOnlyParam()
	m.reportMissingPositionalOnlyArgs(skippedArgsParam, foundUnpackedListArg)

	if !m.reportedArgError {
		m.matchKeywordArgs(foundUnpackedListArg)
	}

	m.combineVariadicArgs()

	// The original's comment: special-case the builtin isinstance and issubclass
	// functions.
	if FunctionTypeIsBuiltIn(overload, "isinstance", "issubclass") && len(m.argParams) == 2 {
		m.argParams[1].IsinstanceParam = true
	}

	return &MatchArgsToParamsResult{
		Overload:                   overload,
		OverloadIndex:              overloadIndex,
		ArgumentErrors:             m.reportedArgError,
		IsTypeIncomplete:           m.isTypeIncomplete,
		ArgParams:                  m.argParams,
		ParamSpecTarget:            m.paramSpecTarget,
		ParamSpecArgList:           m.paramSpecArgList,
		ActiveParam:                m.activeParam,
		UnpackedArgOfUnknownLength: m.unpackedArgOfUnknownLength,
		UnpackedArgMapsToVariadic:  m.unpackedArgMapsToVariadic,
		ArgumentMatchScore:         0,
	}
}

// computePositionalArgCount is the original's `argList.findIndex(...)`.
//
// Its comment: determine how many positional args are being passed before we see
// a keyword arg.
func (m *argMatcher) computePositionalArgCount() {
	m.positionalArgCount = len(m.argList)
	for i, arg := range m.argList {
		if arg.ArgCategory == parser.ArgCategoryUnpackedDictionary || arg.Name != nil {
			m.positionalArgCount = i
			return
		}
	}
}

// detectParamSpec is the original's `varArgListParamIndex/varArgDictParamIndex`
// block.
//
// Its comment: is this an function that uses the *args and **kwargs from a param
// spec? If so, we need to treat all positional parameters prior to the *args as
// positional-only according to PEP 612.
func (m *argMatcher) detectParamSpec() {
	varArgListParamIndex := m.paramDetails.ArgsIndex
	varArgDictParamIndex := m.paramDetails.KwargsIndex

	if varArgListParamIndex != nil && varArgDictParamIndex != nil {
		assert(*varArgListParamIndex < len(m.paramDetails.Params),
			"varArgListParamIndex params entry is undefined")
		varArgListParamType := m.paramDetails.Params[*varArgListParamIndex].Type
		assert(*varArgDictParamIndex < len(m.paramDetails.Params),
			"varArgDictParamIndex params entry is undefined")
		varArgDictParamType := m.paramDetails.Params[*varArgDictParamIndex].Type

		listSpec, listIsSpec := varArgListParamType.(*TypeVarType)
		dictSpec, dictIsSpec := varArgDictParamType.(*TypeVarType)

		if listIsSpec && dictIsSpec && IsParamSpec(varArgListParamType) && IsParamSpec(varArgDictParamType) &&
			listSpec.Priv.ParamSpecAccess == ParamSpecAccessArgs &&
			dictSpec.Priv.ParamSpecAccess == ParamSpecAccessKwargs &&
			listSpec.Shared.Name == dictSpec.Shared.Name {
			m.hasParamSpecArgsKwargs = true

			// The original's comment: does this function define the param spec, or is
			// it an inner function nested within another function that defines the
			// param spec? We need to handle these two cases differently.
			paramSpecScopeId := listSpec.Priv.ScopeID

			if containsScopeID(GetTypeVarScopeIDs(m.overload), paramSpecScopeId) {
				m.paramSpecArgList = []*Arg{}
				m.paramSpecTarget = TypeVarTypeCloneForParamSpecAccess(listSpec, ParamSpecAccessNone)
			} else {
				m.positionalOnlyLimitIndex = *varArgListParamIndex
				if *varArgListParamIndex < m.positionalArgCount {
					m.positionalArgCount = *varArgListParamIndex
				}
				m.positionParamLimitIndex = *varArgListParamIndex
			}
		}
		return
	}

	if m.paramSpec != nil && containsScopeID(GetTypeVarScopeIDs(m.overload), m.paramSpec.Priv.ScopeID) {
		m.hasParamSpecArgsKwargs = true
		m.paramSpecArgList = []*Arg{}
		m.paramSpecTarget = m.paramSpec
	}
}

// containsScopeID is the original's `.some((id) => id === scopeId)`.
func containsScopeID(scopeIds []TypeVarScopeId, scopeID TypeVarScopeId) bool {
	for _, id := range scopeIds {
		if id == scopeID {
			return true
		}
	}
	return false
}

// limitPositionalsForKeywordArgs is the original's block whose comment reads: if
// there are keyword arguments present after a *args argument, the keyword
// arguments may target one or more parameters that are positional. In this case,
// we will limit the number of positional parameters so the *args doesn't consume
// them all.
func (m *argMatcher) limitPositionalsForKeywordArgs() {
	hasUnpackedList := false
	for _, arg := range m.argList {
		if arg.ArgCategory == parser.ArgCategoryUnpackedList {
			hasUnpackedList = true
			break
		}
	}
	if !hasUnpackedList {
		return
	}

	for _, arg := range m.argList {
		if arg.Name == nil {
			continue
		}

		keywordParamIndex := -1
		for i, paramInfo := range m.paramDetails.Params {
			if paramInfo.Param.Name != nil && *paramInfo.Param.Name == arg.Name.D.Value &&
				paramInfo.Param.Category == parser.ParamCategorySimple {
				keywordParamIndex = i
				break
			}
		}

		// The original's comment: is this a parameter that can be interpreted as
		// either a keyword or a positional? If so, we'll treat it as a keyword
		// parameter in this case because it's being targeted by a keyword argument.
		if keywordParamIndex >= 0 && keywordParamIndex >= m.positionalOnlyLimitIndex {
			if m.positionParamLimitIndex < 0 || keywordParamIndex < m.positionParamLimitIndex {
				m.positionParamLimitIndex = keywordParamIndex
			}
		}
	}
}

// matchPositionalArgs is the original's "map the positional args to parameters"
// loop.
func (m *argMatcher) matchPositionalArgs() {
	for m.argIndex < m.positionalArgCount {
		if m.argIndex < m.positionalOnlyLimitIndex && m.argList[m.argIndex].Name != nil {
			nameNode := m.argList[m.argIndex].Name
			m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.ArgPositional(), nameNode, nil)
			m.reportedArgError = true
		}

		remainingArgCount := m.positionalArgCount - m.argIndex
		remainingParamCount := m.positionParamLimitIndex - m.paramIndex - 1

		if m.paramIndex >= m.positionParamLimitIndex {
			m.handleTooManyPositionals()
			break
		}

		if m.paramIndex >= len(m.paramDetails.Params) {
			break
		}

		paramInfo := m.paramDetails.Params[m.paramIndex]
		isParamVariadic := paramInfo.Param.Category == parser.ParamCategoryArgsList && IsUnpacked(paramInfo.Type)

		switch {
		case m.argList[m.argIndex].ArgCategory == parser.ArgCategoryUnpackedList:
			m.matchUnpackedPositional(paramInfo, isParamVariadic, remainingArgCount, remainingParamCount)

		case paramInfo.Param.Category == parser.ParamCategoryArgsList:
			m.matchAgainstArgsList(paramInfo, remainingArgCount, remainingParamCount)

		default:
			m.matchSimplePositional(paramInfo)
		}
	}
}

// handleTooManyPositionals is the `paramIndex >= positionParamLimitIndex` arm.
func (m *argMatcher) handleTooManyPositionals() {
	if m.paramSpecArgList != nil {
		// The original's comment: push the remaining positional args onto the param
		// spec arg list.
		for m.argIndex < m.positionalArgCount {
			m.paramSpecArgList = append(m.paramSpecArgList, m.argList[m.argIndex])
			m.argIndex++
		}
		return
	}

	tooManyPositionals := false

	if m.argList[m.argIndex].ArgCategory == parser.ArgCategoryUnpackedList {
		// The original's comment: if this is an unpacked iterable, we will
		// conservatively assume that it might have zero iterations unless we can tell
		// from its type that it definitely has at least one iterable value.
		argType := m.e.GetTypeOfArg(m.argList[m.argIndex], nil).Type

		argClass, isClass := argType.(*ClassType)
		if isClass && IsClassInstance(argType) && IsTupleClass(argClass) &&
			!IsUnboundedTupleClass(argClass) && len(argClass.Priv.TupleTypeArgs) > 0 {
			tooManyPositionals = true
		} else {
			m.unpackedArgOfUnknownLength = true
		}
	} else {
		tooManyPositionals = true
	}

	if tooManyPositionals {
		if m.canReport() {
			m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				m.tooManyPositionalsMessage(),
				errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
		}
		m.reportedArgError = true
	}
}

// tooManyPositionalsMessage is the original's repeated
// `positionParamLimitIndex === 1 ? argPositionalExpectedOne : argPositionalExpectedCount`.
func (m *argMatcher) tooManyPositionalsMessage() string {
	if m.positionParamLimitIndex == 1 {
		return localization.LocMessage.ArgPositionalExpectedOne()
	}
	return localization.LocMessage.ArgPositionalExpectedCount().Format(m.positionParamLimitIndex)
}

// errorNodeOr is the original's `arg.valueExpression ?? errorNode`.
func errorNodeOr(valueExpression parser.ExpressionNode, errorNode parser.ExpressionNode) parser.ExpressionNode {
	if valueExpression != nil {
		return valueExpression
	}
	return errorNode
}

// matchUnpackedPositional is the `argCategory === UnpackedList` arm.
func (m *argMatcher) matchUnpackedPositional(
	paramInfo *VirtualParamDetails, isParamVariadic bool, remainingArgCount, remainingParamCount int,
) {
	isArgCompatibleWithVariadic := false
	argTypeResult := m.e.GetTypeOfArg(m.argList[m.argIndex], nil)

	var listElementType Type
	enforceIterable := false
	advanceToNextArg := false

	// The original's comment: handle the case where *args is being passed to a
	// function defined with a ParamSpec and a Concatenate operator. PEP 612
	// indicates that all positional parameters specified in the Concatenate must be
	// filled explicitly.
	if m.paramIndex < m.positionParamLimitIndex {
		if spec, ok := argTypeResult.Type.(*TypeVarType); ok && IsParamSpec(argTypeResult.Type) &&
			spec.Priv.ParamSpecAccess == ParamSpecAccessArgs &&
			paramInfo.Param.Category != parser.ParamCategoryArgsList {
			if m.canReport() {
				m.e.AddDiagnostic(DiagnosticRuleReportCallIssue, m.tooManyPositionalsMessage(),
					errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
			}
			m.reportedArgError = true
		}
	}

	argType := argTypeResult.Type
	argClass, argIsClass := argType.(*ClassType)

	switch {
	case isParamVariadic && IsUnpackedTypeVarTuple(argType):
		// The original's comment: allow an unpacked TypeVarTuple arg to satisfy an
		// unpacked TypeVarTuple param.
		listElementType = argType
		isArgCompatibleWithVariadic = true
		advanceToNextArg = true
		m.isTypeVarTupleFullyMatched = true

	case argIsClass && IsClassInstance(argType) && IsTupleClass(argClass) &&
		len(argClass.Priv.TupleTypeArgs) == 1 &&
		IsUnpackedTypeVarTuple(argClass.Priv.TupleTypeArgs[0].Type):
		// The original's comment: handle the case where an unpacked TypeVarTuple has
		// been packaged into a tuple.
		listElementType = argClass.Priv.TupleTypeArgs[0].Type
		isArgCompatibleWithVariadic = true
		advanceToNextArg = true
		m.isTypeVarTupleFullyMatched = true

	case isParamVariadic && argIsClass && IsClassInstance(argType) && IsTupleClass(argClass):
		// The original's comment: handle the case where an unpacked tuple argument is
		// matched to a TypeVarTuple parameter.
		isArgCompatibleWithVariadic = true
		advanceToNextArg = true

		// The original's comment: determine whether we should treat the variadic type
		// as fully matched. This depends on how many args and unmatched parameters
		// exist.
		if remainingArgCount < remainingParamCount {
			m.isTypeVarTupleFullyMatched = true
		}

		listElementType = ClassTypeCloneForUnpacked(argClass)

	case IsParamSpec(argType) && argIsParamSpecArgs(argType):
		listElementType = nil

	default:
		if iterator := m.e.GetTypeOfIterator(
			&TypeResult{Type: argType, IsIncomplete: argTypeResult.IsIncomplete},
			false, m.errorNode, boolPtr(false)); iterator != nil {
			listElementType = iterator.Type
		}

		if listElementType == nil {
			enforceIterable = true
		}

		m.unpackedArgOfUnknownLength = true

		if paramInfo.Param.Category == parser.ParamCategoryArgsList {
			m.unpackedArgMapsToVariadic = true
		}

		if isParamVariadic && listElementType != nil {
			isArgCompatibleWithVariadic = true
			listElementType = MakeTupleObject(m.e,
				[]*TupleTypeArg{{Type: listElementType, IsUnbounded: true}}, true)
		}
	}

	var funcArg *Arg
	if listElementType != nil {
		funcArg = &Arg{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult:  &TypeResult{Type: listElementType, IsIncomplete: argTypeResult.IsIncomplete},
		}
	} else {
		copied := *m.argList[m.argIndex]
		copied.EnforceIterable = enforceIterable
		funcArg = &copied
	}

	if argTypeResult.IsIncomplete {
		m.isTypeIncomplete = true
	}

	// The original's comment: it's not allowed to use unpacked arguments with a
	// variadic *args parameter unless the argument is a variadic arg as well.
	if isParamVariadic && !isArgCompatibleWithVariadic {
		if m.canReport() {
			m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.UnpackedArgWithVariadicParam(),
				errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
		}
		m.reportedArgError = true
	} else {
		if m.paramSpecArgList != nil && paramInfo.Param.Category != parser.ParamCategorySimple {
			m.paramSpecArgList = append(m.paramSpecArgList, m.argList[m.argIndex])
		}

		m.argParams = append(m.argParams, &ValidateArgTypeParams{
			ParamCategory:           paramInfo.Param.Category,
			ParamType:               paramInfo.Type,
			RequiresTypeVarMatching: RequiresSpecialization(paramInfo.Type, nil, 0),
			Argument:                funcArg,
			ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
			ParamName:               derefStr(paramInfo.Param.Name),
			IsParamNameSynthesized:  FunctionParamIsNameSynthesized(paramInfo.Param),
			MapsToVarArgList:        isParamVariadic && remainingArgCount > remainingParamCount,
		})
	}

	m.trySetActive(m.argList[m.argIndex], m.paramDetails.Params[m.paramIndex].Param)

	// The original's comment: note that the parameter has received an argument.
	if paramInfo.Param.Name != nil &&
		m.paramDetails.Params[m.paramIndex].Param.Category == parser.ParamCategorySimple {
		m.paramTracker.MarkArgReceived(paramInfo)
	}

	if advanceToNextArg ||
		m.paramDetails.Params[m.paramIndex].Param.Category == parser.ParamCategoryArgsList {
		m.argIndex++
	}

	if m.isTypeVarTupleFullyMatched ||
		m.paramDetails.Params[m.paramIndex].Param.Category != parser.ParamCategoryArgsList {
		m.paramIndex++
	}
}

// argIsParamSpecArgs is the original's `isParamSpec(argType) &&
// argType.priv.paramSpecAccess === 'args'`.
func argIsParamSpecArgs(argType Type) bool {
	spec, ok := argType.(*TypeVarType)
	return ok && spec.Priv.ParamSpecAccess == ParamSpecAccessArgs
}

// matchAgainstArgsList is the arm where the PARAMETER is `*args` and the
// argument is an ordinary positional.
func (m *argMatcher) matchAgainstArgsList(
	paramInfo *VirtualParamDetails, remainingArgCount, remainingParamCount int,
) {
	m.trySetActive(m.argList[m.argIndex], m.paramDetails.Params[m.paramIndex].Param)

	if m.paramSpecArgList != nil {
		m.paramSpecArgList = append(m.paramSpecArgList, m.argList[m.argIndex])
		m.argIndex++
		return
	}

	paramType := paramInfo.Type
	effectiveParamType := paramType
	paramName := m.paramDetails.Params[m.paramIndex].Param.Name

	if paramClass, ok := paramType.(*ClassType); ok && IsUnpackedClass(paramType) &&
		len(paramClass.Priv.TupleTypeArgs) > 0 {
		effectiveParamType = paramClass.Priv.TupleTypeArgs[0].Type
	}

	paramCategory := parser.ParamCategorySimple
	if IsUnpacked(effectiveParamType) {
		paramCategory = parser.ParamCategoryArgsList
	}

	if remainingArgCount <= remainingParamCount {
		if remainingArgCount < remainingParamCount {
			if m.canReport() {
				// The original's comment: have we run out of arguments and still have
				// parameters left to fill?
				message := localization.LocMessage.ArgMorePositionalExpectedCount().Format(remainingArgCount)
				if remainingArgCount == 1 {
					message = localization.LocMessage.ArgMorePositionalExpectedOne()
				}
				m.e.AddDiagnostic(DiagnosticRuleReportCallIssue, message,
					errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
			}
			m.reportedArgError = true
		}

		m.paramIndex++
		return
	}

	m.argParams = append(m.argParams, &ValidateArgTypeParams{
		ParamCategory:           paramCategory,
		ParamType:               effectiveParamType,
		RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
		Argument:                m.argList[m.argIndex],
		ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
		ParamName:               derefStr(paramName),
		IsParamNameSynthesized:  FunctionParamIsNameSynthesized(m.paramDetails.Params[m.paramIndex].Param),
		MapsToVarArgList:        true,
	})

	m.argIndex++
}

// matchSimplePositional is the ordinary one-argument-to-one-parameter arm.
func (m *argMatcher) matchSimplePositional(paramInfo *VirtualParamDetails) {
	m.argParams = append(m.argParams, &ValidateArgTypeParams{
		ParamCategory:           paramInfo.Param.Category,
		ParamType:               paramInfo.Type,
		RequiresTypeVarMatching: RequiresSpecialization(paramInfo.Type, nil, 0),
		Argument:                m.argList[m.argIndex],
		ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
		ParamName:               derefStr(paramInfo.Param.Name),
		IsParamNameSynthesized:  FunctionParamIsNameSynthesized(paramInfo.Param),
	})
	m.trySetActive(m.argList[m.argIndex], paramInfo.Param)

	// The original's comment: note that the parameter has received an argument.
	m.paramTracker.MarkArgReceived(paramInfo)

	m.argIndex++
	m.paramIndex++
}

// skipUnboundedPositionalOnlyParam is the original's block whose comment reads:
// if there weren't enough positional arguments to populate all of the
// positional-only parameters and the next positional-only parameter is an
// unbounded tuple, skip past it.
func (m *argMatcher) skipUnboundedPositionalOnlyParam() bool {
	if m.positionalOnlyLimitIndex >= 0 &&
		m.paramIndex < m.positionalOnlyLimitIndex &&
		m.paramIndex < len(m.paramDetails.Params) &&
		m.paramDetails.Params[m.paramIndex].Param.Category == parser.ParamCategoryArgsList &&
		!IsParamSpec(m.paramDetails.Params[m.paramIndex].Type) {
		m.paramIndex++
		return true
	}
	return false
}

// reportMissingPositionalOnlyArgs is the original's block whose comment reads:
// check if there weren't enough positional arguments to populate all of the
// positional-only parameters.
func (m *argMatcher) reportMissingPositionalOnlyArgs(skippedArgsParam, foundUnpackedListArg bool) {
	if m.positionalOnlyLimitIndex < 0 || m.paramIndex >= m.positionalOnlyLimitIndex {
		return
	}
	if foundUnpackedListArg && !m.hasParamSpecArgsKwargs {
		return
	}

	firstParamWithDefault := -1
	for i, paramInfo := range m.paramDetails.Params {
		if paramInfo.DefaultType != nil {
			firstParamWithDefault = i
			break
		}
	}

	positionOnlyWithoutDefaultsCount := m.positionalOnlyLimitIndex
	if firstParamWithDefault >= 0 && firstParamWithDefault < m.positionalOnlyLimitIndex {
		positionOnlyWithoutDefaultsCount = firstParamWithDefault
	}

	// The original's comment: calculate the number of remaining positional
	// parameters to report.
	argsRemainingCount := positionOnlyWithoutDefaultsCount - m.positionalArgCount
	if skippedArgsParam {
		// The original's comment: if we skipped an args parameter above, reduce the
		// count by one because it's permitted to pass zero arguments to *args.
		argsRemainingCount--
	}

	firstArgsParam := -1
	for i, paramInfo := range m.paramDetails.Params {
		if paramInfo.Param.Category == parser.ParamCategoryArgsList && !IsParamSpec(paramInfo.Type) {
			firstArgsParam = i
			break
		}
	}
	if firstArgsParam >= m.paramIndex && firstArgsParam < m.positionalOnlyLimitIndex {
		// The original's comment: if there is another args parameter beyond the
		// current param index, reduce the count by one because it's permitted to pass
		// zero arguments to *args.
		argsRemainingCount--
	}

	if argsRemainingCount <= 0 {
		return
	}

	if m.canReport() {
		message := localization.LocMessage.ArgMorePositionalExpectedCount().Format(argsRemainingCount)
		if argsRemainingCount == 1 {
			message = localization.LocMessage.ArgMorePositionalExpectedOne()
		}

		reportNode := m.errorNode
		if len(m.argList) > m.positionalArgCount {
			reportNode = errorNodeOr(m.argList[m.positionalArgCount].ValueExpression, m.errorNode)
		}

		m.e.AddDiagnostic(DiagnosticRuleReportCallIssue, message, reportNode, nil)
	}
	m.reportedArgError = true
}

// matchKeywordArgs is the original's "now consume any keyword arguments" loop
// plus the two checks that follow it.
func (m *argMatcher) matchKeywordArgs(foundUnpackedListArg bool) {
	var unpackedDictKeyNames []string
	unpackedDictKeyNamesValid := false
	var unpackedDictArgType Type

	for m.argIndex < len(m.argList) {
		if m.argList[m.argIndex].ArgCategory == parser.ArgCategoryUnpackedDictionary {
			names, namesValid, argType := m.matchUnpackedDictArg()
			if argType != nil {
				unpackedDictArgType = argType
			}
			if namesValid {
				unpackedDictKeyNames = names
				unpackedDictKeyNamesValid = true
			}

			if m.paramSpecArgList != nil {
				m.paramSpecArgList = append(m.paramSpecArgList, m.argList[m.argIndex])
			}
		} else {
			m.matchNamedOrExtraArg()
		}

		m.argIndex++
	}

	m.applyUnpackedDictToKeywordParams(unpackedDictArgType, unpackedDictKeyNames,
		unpackedDictKeyNamesValid, foundUnpackedListArg)
	m.reportUnassignedParams(unpackedDictArgType)
}

// reportUnassignedParams is the original's block whose comment reads: determine
// whether there are any parameters that require arguments but have not yet
// received them. If we received a dictionary argument (i.e. an arg starting with
// a "**"), we will assume that all parameters are matched.
func (m *argMatcher) reportUnassignedParams(unpackedDictArgType Type) {
	if unpackedDictArgType != nil || FunctionTypeIsDefaultParamCheckDisabled(m.overload) {
		return
	}

	unassignedParams := m.paramTracker.GetUnassignedParams()

	if len(unassignedParams) > 0 {
		if !m.e.canSkipDiagnosticForNode(m.errorNode) {
			missingParamNames := quoteAndJoin(unassignedParams)
			if m.canReport() {
				message := localization.LocMessage.ArgMissingForParams().Format(missingParamNames)
				if len(unassignedParams) == 1 {
					message = localization.LocMessage.ArgMissingForParam().Format(missingParamNames)
				}
				m.e.AddDiagnostic(DiagnosticRuleReportCallIssue, message, m.errorNode, nil)
			}
		}
		m.reportedArgError = true
	}

	// The original's comment: add any implicit (default) arguments that are needed
	// for resolving generic types. For example, if the function is defined as
	// def foo(v1: _T = 'default') and _T is a TypeVar, we need to match the TypeVar
	// to the default value's type if it's not provided by the caller.
	for _, paramInfo := range m.paramDetails.Params {
		param := paramInfo.Param
		if param.Category != parser.ParamCategorySimple || param.Name == nil {
			continue
		}

		entry := m.paramTracker.LookupDetails(paramInfo)
		if entry.ArgsNeeded != 0 || entry.ArgsReceived != 0 {
			continue
		}

		defaultArgType := paramInfo.DefaultType
		if defaultArgType == nil || IsEllipsisType(defaultArgType) ||
			!RequiresSpecialization(paramInfo.DeclaredType,
				&RequiresSpecializationOptions{IgnorePseudoGeneric: true}, 0) {
			continue
		}

		m.argParams = append(m.argParams, &ValidateArgTypeParams{
			ParamCategory:           param.Category,
			ParamType:               paramInfo.Type,
			RequiresTypeVarMatching: true,
			Argument: &Arg{
				ArgCategory: parser.ArgCategorySimple,
				TypeResult:  &TypeResult{Type: defaultArgType},
			},
			IsDefaultArg:           true,
			ErrorNode:              m.errorNode,
			ParamName:              derefStr(param.Name),
			IsParamNameSynthesized: FunctionParamIsNameSynthesized(param),
		})
	}
}

// quoteAndJoin is the original's `.map((p) => `"${p}"`).join(', ')`.
func quoteAndJoin(names []string) string {
	result := ""
	for i, name := range names {
		if i > 0 {
			result += ", "
		}
		result += `"` + name + `"`
	}
	return result
}

// combineVariadicArgs is the original's final block, whose comment reads: if
// there are arguments that map to a variadic *args parameter that hasn't already
// been matched, see if the type of that *args parameter is a TypeVarTuple. If so,
// we'll preprocess those arguments and combine them into a tuple.
func (m *argMatcher) combineVariadicArgs() {
	// The original's comment: if we're in speculative mode and an arg/param
	// mismatch has already been reported, don't bother doing the extra work here.
	// This occurs frequently when attempting to find the correct overload.
	if m.reportedArgError && m.e.IsSpeculativeModeInUse(nil) {
		return
	}

	assert(m.paramDetails.ArgsIndex == nil || *m.paramDetails.ArgsIndex < len(m.paramDetails.Params),
		"paramDetails.argsIndex params entry is invalid")

	if m.paramDetails.ArgsIndex == nil || *m.paramDetails.ArgsIndex < 0 ||
		!FunctionParamIsTypeDeclared(m.paramDetails.Params[*m.paramDetails.ArgsIndex].Param) ||
		m.isTypeVarTupleFullyMatched {
		return
	}

	paramType := m.paramDetails.Params[*m.paramDetails.ArgsIndex].Type

	if !IsUnpacked(paramType) {
		return
	}
	if tvt, ok := paramType.(*TypeVarType); ok && IsTypeVarTuple(paramType) && tvt.Priv.IsInUnion {
		return
	}

	variadicArgs := []*ValidateArgTypeParams{}
	for _, argParam := range m.argParams {
		if argParam.MapsToVarArgList {
			variadicArgs = append(variadicArgs, argParam)
		}
	}

	tupleTypeArgs := make([]*TupleTypeArg, len(variadicArgs))
	for i, argParam := range variadicArgs {
		argType := m.e.GetTypeOfArg(argParam.Argument, nil).Type

		containsTypeVarTuple := IsUnpackedTypeVarTuple(argType)
		if !containsTypeVarTuple {
			if argClass, ok := argType.(*ClassType); ok && IsClassInstance(argType) &&
				IsTupleClass(argClass) && len(argClass.Priv.TupleTypeArgs) == 1 &&
				IsUnpackedTypeVarTuple(argClass.Priv.TupleTypeArgs[0].Type) {
				containsTypeVarTuple = true
			}
		}

		if containsTypeVarTuple && argParam.Argument.ArgCategory != parser.ArgCategoryUnpackedList &&
			!argParam.MapsToVarArgList {
			if m.canReport() {
				m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
					localization.LocMessage.TypeVarTupleMustBeUnpacked(),
					errorNodeOr(argParam.Argument.ValueExpression, m.errorNode), nil)
			}
			m.reportedArgError = true
		}

		tupleTypeArgs[i] = &TupleTypeArg{
			Type:        argType,
			IsUnbounded: argParam.Argument.ArgCategory == parser.ArgCategoryUnpackedList,
		}
	}

	var specializedTuple Type
	if len(tupleTypeArgs) == 1 && !tupleTypeArgs[0].IsUnbounded {
		if IsUnpacked(tupleTypeArgs[0].Type) {
			specializedTuple = MakePacked(tupleTypeArgs[0].Type)
		}
	}

	if specializedTuple == nil {
		specializedTuple = MakeTupleObject(m.e, tupleTypeArgs, false)
	}

	combinedArg := &ValidateArgTypeParams{
		ParamCategory:           parser.ParamCategorySimple,
		ParamType:               MakePacked(paramType),
		RequiresTypeVarMatching: true,
		Argument: &Arg{
			ArgCategory: parser.ArgCategorySimple,
			TypeResult:  &TypeResult{Type: specializedTuple},
		},
		ErrorNode:              m.errorNode,
		ParamName:              derefStr(m.paramDetails.Params[*m.paramDetails.ArgsIndex].Param.Name),
		IsParamNameSynthesized: FunctionParamIsNameSynthesized(m.paramDetails.Params[*m.paramDetails.ArgsIndex].Param),
		MapsToVarArgList:       true,
	}

	remaining := []*ValidateArgTypeParams{}
	for _, argParam := range m.argParams {
		if !argParam.MapsToVarArgList {
			remaining = append(remaining, argParam)
		}
	}
	m.argParams = append(remaining, combinedArg)
}

// GetAnyOrUnknownInInvariantPosition corresponds to the function of the same
// name. It answers whether an Any or Unknown appears somewhere the caller cannot
// safely ignore -- an invariant type argument, or a tuple entry that contains
// one. It returns nil where the original returns undefined.
func (e *typeEvaluator) getAnyOrUnknownInInvariantPosition(t Type, recursionCount int) Type {
	if recursionCount > MaxTypeRecursionCount {
		return nil
	}
	recursionCount++

	var result Type
	addResult := func(newResult Type) {
		if newResult == nil {
			return
		}
		if result != nil {
			result = PreserveUnknown(result, newResult)
		} else {
			result = newResult
		}
	}

	if IsUnion(t) {
		DoForEachSubtype(t, func(subtype Type, _ int, _ []Type) {
			addResult(e.getAnyOrUnknownInInvariantPosition(subtype, recursionCount))
		})
		return result
	}

	classType, ok := t.(*ClassType)
	if !ok || !IsClass(t) {
		return nil
	}

	// The original's comment: tuple entries are covariant, but they can contain an
	// invariant type.
	if classType.Priv.TupleTypeArgs != nil {
		for _, typeArg := range classType.Priv.TupleTypeArgs {
			addResult(e.getAnyOrUnknownInInvariantPosition(typeArg.Type, recursionCount))
		}
		return result
	}

	if classType.Priv.TypeArgs == nil {
		return nil
	}

	e.InferVarianceForClass(classType)
	typeParams := ClassTypeGetTypeParams(classType)

	for index, typeArg := range classType.Priv.TypeArgs {
		variance := VarianceInvariant
		if index < len(typeParams) {
			variance = TypeVarTypeGetVariance(typeParams[index])
		}

		if variance == VarianceInvariant {
			addResult(ContainsAnyOrUnknown(typeArg, true))
		} else {
			addResult(e.getAnyOrUnknownInInvariantPosition(typeArg, recursionCount))
		}
	}

	return result
}

// unusedDiagnosticAddendum keeps the common import referenced from this file
// even when the diagnostic paths above are all inlined.
var _ = common.NewDiagnosticAddendum

// matchUnpackedDictArg is the original's `**kwargs` arm of the keyword loop. It
// returns the literal key names when they are all known (with the second result
// reporting whether they are), and the value type to spread across unmatched
// keyword parameters.
func (m *argMatcher) matchUnpackedDictArg() ([]string, bool, Type) {
	// The original's comment: verify that the type used in this expression is a
	// SupportsKeysAndGetItem[str, T].
	argTypeResult := m.e.GetTypeOfArg(m.argList[m.argIndex],
		MakeInferenceContext(m.paramDetails.UnpackedKwargsTypedDictType, false, nil))
	argType := argTypeResult.Type

	if argTypeResult.IsIncomplete {
		m.isTypeIncomplete = true
	}

	if IsAnyOrUnknown(argType) {
		return nil, false, argType
	}

	if argClass, ok := argType.(*ClassType); ok && IsClassInstance(argType) &&
		ClassTypeIsTypedDictClass(argClass) {
		m.matchTypedDictKwargs(argClass)
		return nil, false, nil
	}

	if m.paramSpec != nil && IsParamSpecKwargs(m.paramSpec, argType) {
		if m.paramSpecArgList == nil {
			var argTypeOverride Type
			if !IsParamSpec(argType) {
				argTypeOverride = AnyTypeCreate(false)
			}
			m.argParams = append(m.argParams, &ValidateArgTypeParams{
				ParamCategory:           parser.ParamCategoryKwargsDict,
				ParamType:               m.paramSpec,
				RequiresTypeVarMatching: false,
				Argument:                m.argList[m.argIndex],
				ArgType:                 argTypeOverride,
				ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
			})
		}
		return nil, false, AnyTypeCreate(false)
	}

	return m.matchMappingKwargs(argType)
}

// matchTypedDictKwargs is the original's TypedDict branch, whose comment reads:
// handle the special case where it is a TypedDict and we know which keys are
// present.
func (m *argMatcher) matchTypedDictKwargs(argClass *ClassType) {
	tdEntries := GetTypedDictMembersForClass(m.e, argClass, false)
	diag := common.NewDiagnosticAddendum()

	tdEntries.KnownItems.ForEach(func(entry *TypedDictEntry, name string) {
		paramEntry := m.paramTracker.LookupName(name)

		if paramEntry != nil {
			if paramEntry.ArgsReceived > 0 {
				diag.AddMessage(localization.LocMessage.ParamAlreadyAssigned().Format(name))
				return
			}

			paramEntry.ArgsReceived++

			paramInfoIndex := -1
			for i, paramInfo := range m.paramDetails.Params {
				if paramInfo.Param.Name != nil && *paramInfo.Param.Name == name {
					paramInfoIndex = i
					break
				}
			}
			assert(paramInfoIndex >= 0, "expected to find the parameter")
			paramType := m.paramDetails.Params[paramInfoIndex].Type

			nameCopy := name
			m.argParams = append(m.argParams, &ValidateArgTypeParams{
				ParamCategory:           parser.ParamCategorySimple,
				ParamType:               paramType,
				RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
				Argument: &Arg{
					ArgCategory: parser.ArgCategorySimple,
					TypeResult:  &TypeResult{Type: entry.ValueType},
				},
				ErrorNode: errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
				ParamName: nameCopy,
			})
			return
		}

		if m.paramDetails.KwargsIndex != nil {
			paramType := m.paramDetails.Params[*m.paramDetails.KwargsIndex].Type
			nameCopy := name
			m.argParams = append(m.argParams, &ValidateArgTypeParams{
				ParamCategory:           parser.ParamCategoryKwargsDict,
				ParamType:               paramType,
				RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
				Argument: &Arg{
					ArgCategory: parser.ArgCategorySimple,
					TypeResult:  &TypeResult{Type: entry.ValueType},
				},
				ErrorNode: errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
				ParamName: nameCopy,
			})

			// The original's comment: remember that this parameter has already
			// received a value.
			m.paramTracker.AddKeywordParam(name, m.paramDetails.Params[*m.paramDetails.KwargsIndex])
			return
		}

		// The original's comment: if the function doesn't have a **kwargs parameter,
		// we need to emit an error. However, it's possible that there was a **kwargs
		// but it was eliminated by getParamListDetails because it was associated with
		// an unpacked TypedDict. In this case, we can skip the error.
		if !m.paramDetails.HasUnpackedTypedDict {
			diag.AddMessage(localization.LocMessage.ParamNameMissing().Format(name))
		}
	})

	extraItemsType := m.e.GetObjectType()
	if tdEntries.ExtraItems != nil {
		extraItemsType = tdEntries.ExtraItems.ValueType
	}

	if !IsNever(extraItemsType) && m.paramDetails.KwargsIndex != nil {
		kwargsParam := m.paramDetails.Params[*m.paramDetails.KwargsIndex]

		m.argParams = append(m.argParams, &ValidateArgTypeParams{
			ParamCategory:           parser.ParamCategoryKwargsDict,
			ParamType:               kwargsParam.Type,
			RequiresTypeVarMatching: RequiresSpecialization(kwargsParam.Type, nil, 0),
			Argument: &Arg{
				ArgCategory: parser.ArgCategoryUnpackedDictionary,
				TypeResult:  &TypeResult{Type: extraItemsType},
			},
			ErrorNode: errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
			ParamName: derefStr(kwargsParam.Param.Name),
		})
	}

	if !diag.IsEmpty() {
		if m.canReport() {
			m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.UnpackedTypedDictArgument()+diag.GetString(),
				errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
		}
		m.reportedArgError = true
	}
}

// matchMappingKwargs is the original's general `**mapping` branch, which checks
// that the argument satisfies SupportsKeysAndGetItem[str, T] and pulls T out.
func (m *argMatcher) matchMappingKwargs(argType Type) ([]string, bool, Type) {
	strObjType := m.e.GetBuiltInObject(m.errorNode, "str", nil)

	if m.e.prefetched == nil || m.e.prefetched.SupportsKeysAndGetItemClass == nil ||
		!IsInstantiableClass(m.e.prefetched.SupportsKeysAndGetItemClass) ||
		strObjType == nil || !IsClassInstance(strObjType) {
		return nil, false, nil
	}

	supportsClass := m.e.prefetched.SupportsKeysAndGetItemClass.(*ClassType)
	mappingConstraints := NewConstraintTracker()
	isValidMappingType := false
	var unpackedDictArgType Type
	var unpackedDictKeyNames []string
	unpackedDictKeyNamesValid := false

	// The original's comment: if this was a TypeVar (e.g. for pseudo-generic
	// classes), don't emit this error.
	if IsTypeVar(argType) {
		isValidMappingType = true
	} else if m.e.AssignType(ClassTypeCloneAsInstance(supportsClass, false), argType,
		nil, mappingConstraints, AssignTypeFlagsDefault, 0) {
		solved := m.e.SolveAndApplyConstraints(supportsClass, mappingConstraints, nil, nil)
		specializedMapping, ok := solved.(*ClassType)
		typeArgs := []Type{}
		if ok {
			typeArgs = specializedMapping.Priv.TypeArgs
		}

		if len(typeArgs) >= 2 {
			if m.e.AssignType(strObjType, typeArgs[0], nil, nil, AssignTypeFlagsDefault, 0) {
				isValidMappingType = true
			}

			unpackedDictKeyNames = []string{}
			unpackedDictKeyNamesValid = true
			DoForEachSubtype(typeArgs[0], func(keyType Type, _ int, _ []Type) {
				if keyClass, ok := keyType.(*ClassType); ok && IsClassInstance(keyType) {
					if literal, isStr := keyClass.Priv.LiteralValue.(LiteralString); isStr {
						unpackedDictKeyNames = append(unpackedDictKeyNames, string(literal))
						return
					}
				}
				unpackedDictKeyNamesValid = false
			})

			unpackedDictArgType = typeArgs[1]
		} else {
			isValidMappingType = true
			unpackedDictArgType = UnknownTypeCreate(false)
		}
	}

	m.unpackedArgOfUnknownLength = true

	if m.paramDetails.KwargsIndex != nil && unpackedDictArgType != nil {
		paramType := m.paramDetails.Params[*m.paramDetails.KwargsIndex].Type
		m.argParams = append(m.argParams, &ValidateArgTypeParams{
			ParamCategory:           parser.ParamCategorySimple,
			ParamType:               paramType,
			RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
			ArgType:                 unpackedDictArgType,
			Argument:                m.argList[m.argIndex],
			ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
			ParamName:               derefStr(m.paramDetails.Params[*m.paramDetails.KwargsIndex].Param.Name),
		})

		m.unpackedArgMapsToVariadic = true
	}

	if !isValidMappingType {
		if m.canReport() {
			m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
				localization.LocMessage.UnpackedDictArgumentNotMapping(),
				errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
		}
		m.reportedArgError = true
	}

	if !unpackedDictKeyNamesValid {
		return nil, false, unpackedDictArgType
	}
	return unpackedDictKeyNames, true, unpackedDictArgType
}

// matchNamedOrExtraArg is the non-`**` arm of the keyword loop.
//
// The original's comment: protect against the case where a non-keyword argument
// appears after a keyword argument. This will have already been reported as a
// parse error, but we need to protect against it here.
func (m *argMatcher) matchNamedOrExtraArg() {
	paramName := m.argList[m.argIndex].Name

	if paramName != nil {
		m.matchNamedArg(paramName)
		return
	}

	if m.argList[m.argIndex].ArgCategory == parser.ArgCategorySimple {
		if m.paramSpecArgList != nil {
			m.paramSpecArgList = append(m.paramSpecArgList, m.argList[m.argIndex])
			return
		}

		if m.canReport() {
			m.e.AddDiagnostic(DiagnosticRuleReportCallIssue, m.tooManyPositionalsMessage(),
				errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode), nil)
		}
		m.reportedArgError = true
		return
	}

	if m.argList[m.argIndex].ArgCategory == parser.ArgCategoryUnpackedList && m.paramSpec != nil {
		// The original's comment: handle the case where a *args: P.args (or
		// *args: Any) is passed as an argument to a function that accepts a ParamSpec.
		argTypeResult := m.e.GetTypeOfArg(m.argList[m.argIndex], nil)
		argType := argTypeResult.Type

		if argTypeResult.IsIncomplete {
			m.isTypeIncomplete = true
		}

		if IsParamSpecArgs(m.paramSpec, argType) {
			var argTypeOverride Type
			if !IsParamSpec(argType) {
				argTypeOverride = AnyTypeCreate(false)
			}
			m.argParams = append(m.argParams, &ValidateArgTypeParams{
				ParamCategory:           parser.ParamCategoryArgsList,
				ParamType:               m.paramSpec,
				RequiresTypeVarMatching: false,
				Argument:                m.argList[m.argIndex],
				ArgType:                 argTypeOverride,
				ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
			})
		}
	}
}

// matchNamedArg is the `arg.name !== undefined` arm.
func (m *argMatcher) matchNamedArg(paramName *parser.NameNode) {
	paramNameValue := paramName.D.Value
	paramEntry := m.paramTracker.LookupName(paramNameValue)

	if paramEntry != nil {
		if paramEntry.ArgsReceived > 0 {
			if m.canReport() {
				m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
					localization.LocMessage.ParamAlreadyAssigned().Format(paramNameValue), paramName, nil)
			}
			m.reportedArgError = true
			return
		}

		paramEntry.ArgsReceived++

		paramInfoIndex := -1
		for i, paramInfo := range m.paramDetails.Params {
			if paramInfo.Param.Name != nil && *paramInfo.Param.Name == paramNameValue &&
				paramInfo.Kind != ParamKindPositional {
				paramInfoIndex = i
				break
			}
		}
		assert(paramInfoIndex >= 0, "expected to find the keyword parameter")
		paramType := m.paramDetails.Params[paramInfoIndex].Type

		m.argParams = append(m.argParams, &ValidateArgTypeParams{
			ParamCategory:           parser.ParamCategorySimple,
			ParamType:               paramType,
			RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
			Argument:                m.argList[m.argIndex],
			ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
			ParamName:               paramNameValue,
		})
		m.trySetActive(m.argList[m.argIndex], m.paramDetails.Params[paramInfoIndex].Param)
		return
	}

	if m.paramSpecArgList != nil {
		m.paramSpecArgList = append(m.paramSpecArgList, m.argList[m.argIndex])
		return
	}

	if m.paramDetails.KwargsIndex != nil {
		paramType := m.paramDetails.Params[*m.paramDetails.KwargsIndex].Type
		if IsParamSpec(paramType) {
			if m.canReport() {
				m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
					localization.LocMessage.ParamNameMissing().Format(paramName.D.Value), paramName, nil)
			}
			m.reportedArgError = true
		} else {
			m.argParams = append(m.argParams, &ValidateArgTypeParams{
				ParamCategory:           parser.ParamCategoryKwargsDict,
				ParamType:               paramType,
				RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
				Argument:                m.argList[m.argIndex],
				ErrorNode:               errorNodeOr(m.argList[m.argIndex].ValueExpression, m.errorNode),
				ParamName:               paramNameValue,
			})

			// The original's comment: remember that this parameter has already
			// received a value.
			m.paramTracker.AddKeywordParam(paramNameValue, m.paramDetails.Params[*m.paramDetails.KwargsIndex])
		}
		m.trySetActive(m.argList[m.argIndex], m.paramDetails.Params[*m.paramDetails.KwargsIndex].Param)
		return
	}

	if m.canReport() {
		m.e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.ParamNameMissing().Format(paramName.D.Value), paramName, nil)
	}
	m.reportedArgError = true
}

// applyUnpackedDictToKeywordParams is the original's block whose comment reads:
// if there are keyword-only parameters that haven't been matched but we have an
// unpacked dictionary arg, assume that it applies to them.
func (m *argMatcher) applyUnpackedDictToKeywordParams(
	unpackedDictArgType Type, unpackedDictKeyNames []string,
	unpackedDictKeyNamesValid bool, foundUnpackedListArg bool,
) {
	if unpackedDictArgType == nil {
		return
	}
	if foundUnpackedListArg && m.paramDetails.ArgsIndex == nil {
		return
	}

	// The original's comment: don't consider any position-only parameters, since
	// they cannot be matched to **kwargs arguments. Consider parameters that are
	// either positional or keyword if there is no *args argument.
	for paramIndex, paramInfo := range m.paramDetails.Params {
		param := paramInfo.Param
		if paramIndex < m.paramDetails.FirstPositionOrKeywordIndex ||
			param.Category != parser.ParamCategorySimple || param.Name == nil ||
			m.paramTracker.LookupDetails(paramInfo).ArgsReceived != 0 {
			continue
		}

		if unpackedDictKeyNamesValid && !containsString(unpackedDictKeyNames, *param.Name) {
			continue
		}

		paramType := m.paramDetails.Params[paramIndex].Type

		errorNode := m.errorNode
		for _, arg := range m.argList {
			if arg.ArgCategory == parser.ArgCategoryUnpackedDictionary && arg.ValueExpression != nil {
				errorNode = arg.ValueExpression
				break
			}
		}

		m.argParams = append(m.argParams, &ValidateArgTypeParams{
			ParamCategory:           parser.ParamCategorySimple,
			ParamType:               paramType,
			RequiresTypeVarMatching: RequiresSpecialization(paramType, nil, 0),
			Argument: &Arg{
				ArgCategory: parser.ArgCategorySimple,
				TypeResult:  &TypeResult{Type: unpackedDictArgType},
			},
			ErrorNode:              errorNode,
			ParamName:              derefStr(param.Name),
			IsParamNameSynthesized: FunctionParamIsNameSynthesized(param),
		})

		m.paramTracker.MarkArgReceived(m.paramDetails.Params[paramIndex])
	}
}

// containsString is the original's `array.includes(value)`.
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// derefStr is the original's implicit `string | undefined` -> `string`, where
// ValidateArgTypeParams.paramName is optional and an absent name is the empty
// string.
func derefStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
