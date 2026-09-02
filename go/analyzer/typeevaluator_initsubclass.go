/*
 * typeevaluator_initsubclass.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * validateInitSubclassArgs.
 *
 * This checks the keyword arguments in a class header -- `class C(Base,
 * flag=True)` -- against whatever will receive them at runtime. PEP 487 routes
 * them to `__init_subclass__`, but a metaclass that overrides `__new__` takes
 * them first, so which of the two is checked depends on the metaclass.
 *
 * `metaclass=` is excluded from the argument list because it is consumed by the
 * class machinery itself and never reaches either callee.
 *
 * The ABCMeta/type exemption is not an optimization. Those two are known to pass
 * their keyword arguments straight through to `__init_subclass__`, so checking
 * their `__new__` signature would reject arguments that are in fact legal. The
 * TypedDict carve-out inside it is a typeshed artifact, and the original says so:
 * `_TypedDict` declares ABCMeta as its metaclass but does not override
 * `__init_subclass__`, so the pass-through assumption does not hold for it.
 *
 * The two branches report differently on purpose. The metaclass-`__new__` path
 * matches arguments to keyword-only parameters by hand and reports each unknown
 * name and each unsupplied parameter individually, because there is no call node
 * to hang a normal call diagnostic on. The `__init_subclass__` path can use
 * validateCallArgs and gets one aggregate diagnostic.
 *
 * The final loop is load-bearing despite looking redundant: every argument
 * expression is evaluated even when no callee was found, because that is what
 * marks the names inside it as referenced. Skipping it would make an otherwise
 * used symbol report as unaccessed.
 */

package analyzer

import (
	"strings"

	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// validateInitSubclassArgs corresponds to the function of the same name.
func (e *typeEvaluator) validateInitSubclassArgs(node *parser.ClassNode, classType *ClassType) {
	// The original's comment: collect arguments that will be passed to the
	// `__init_subclass__` method described in PEP 487 and validate it.
	argList := []*Arg{}

	for _, arg := range node.D.Arguments {
		if arg.D.Name != nil && arg.D.Name.D.Value != "metaclass" {
			argList = append(argList, &Arg{
				ArgCategory:     parser.ArgCategorySimple,
				Node:            arg,
				Name:            arg.D.Name,
				ValueExpression: arg.D.ValueExpr,
			})
		}
	}

	var newMethodMember *ClassMember

	// The original's comment: see if the class has a metaclass that overrides
	// `__new__`. If so, we will validate the signature of the `__new__` method.
	if classType.Shared.EffectiveMetaclass != nil && IsClass(classType.Shared.EffectiveMetaclass) {
		// The original's comment: if the metaclass is 'type' or 'ABCMeta', we'll
		// assume it will call through to __init_subclass__, so we'll skip the
		// `__new__` method check. We need to exclude TypedDict classes here because
		// _TypedDict uses ABCMeta as its metaclass, but its typeshed definition
		// doesn't override __init_subclass__.
		metaclass := classType.Shared.EffectiveMetaclass.(*ClassType)
		metaclassCallsInitSubclass := ClassTypeIsBuiltInNamed(metaclass, "ABCMeta", "type") &&
			!ClassTypeIsTypedDictClass(classType)

		if !metaclassCallsInitSubclass {
			// The original's comment: see if the metaclass has a `__new__` method
			// that accepts keyword parameters.
			newMethodMember = LookUpClassMember(metaclass, "__new__",
				MemberAccessFlagsSkipTypeBaseClass, nil)
		}
	}

	if newMethodMember != nil {
		e.validateInitSubclassAgainstMetaclassNew(node, argList, newMethodMember)
	} else {
		// The original's comment: if there was no custom metaclass __new__ method,
		// see if there is an __init_subclass__ method present somewhere in the
		// class hierarchy.
		e.validateInitSubclassAgainstInitSubclass(node, classType, argList)
	}

	// The original's comment: evaluate all of the expressions so they are checked
	// and marked referenced.
	for _, arg := range argList {
		if arg.ValueExpression != nil {
			e.GetTypeOfExpression(arg.ValueExpression, EvalFlagsNone, nil)
		}
	}
}

// validateInitSubclassAgainstMetaclassNew is the original's `if (newMethodMember)`
// arm.
func (e *typeEvaluator) validateInitSubclassAgainstMetaclassNew(
	node *parser.ClassNode, argList []*Arg, newMethodMember *ClassMember,
) {
	newMethodType := e.GetTypeOfMember(newMethodMember)
	if !IsFunction(newMethodType) {
		return
	}

	paramListDetails := GetParamListDetails(newMethodType.(*FunctionType), nil)
	if paramListDetails.FirstKeywordOnlyIndex == nil {
		return
	}

	// The original's comment: build a map of the keyword-only parameters.
	// Insertion order matters for the unassigned-parameter diagnostic below, so
	// this is an ordered map rather than a plain one.
	paramMap := newOrderedIndexMap()
	for i := *paramListDetails.FirstKeywordOnlyIndex; i < len(paramListDetails.Params); i++ {
		paramInfo := paramListDetails.Params[i]
		if paramInfo.Param.Category == parser.ParamCategorySimple &&
			paramInfo.Param.Name != nil && paramInfo.Kind != ParamKindPositional {
			paramMap.Set(*paramInfo.Param.Name, i)
		}
	}

	for _, arg := range argList {
		if arg.ArgCategory != parser.ArgCategorySimple || arg.Name == nil {
			continue
		}

		paramIndex, found := paramMap.Get(arg.Name.D.Value)
		if !found && paramListDetails.KwargsIndex != nil {
			// An unrecognized name is legal when the callee has **kwargs; it is
			// checked against the kwargs entry instead of being reported.
			paramIndex, found = *paramListDetails.KwargsIndex, true
		}

		if !found {
			var errorNode parser.ExpressionNode = arg.Name
			if errorNode == nil {
				errorNode = node.D.Name
			}
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.ParamNameMissing().Format(arg.Name.D.Value),
				errorNode,
				nil,
			)
			continue
		}

		paramInfo := paramListDetails.Params[paramIndex]
		errorNode := arg.ValueExpression
		if errorNode == nil {
			errorNode = node.D.Name
		}
		argParam := &ValidateArgTypeParams{
			ParamCategory:           paramInfo.Param.Category,
			ParamType:               paramInfo.Type,
			RequiresTypeVarMatching: false,
			Argument:                arg,
			ErrorNode:               errorNode,
		}

		e.validateArgType(argParam, NewConstraintTracker(), &TypeResult{Type: newMethodType},
			&ValidateArgTypeOptions{SkipUnknownArgCheck: true})
		paramMap.Delete(arg.Name.D.Value)
	}

	// The original's comment: see if we have any remaining unmatched parameters
	// without default values.
	unassignedParams := []string{}
	paramMap.ForEach(func(paramName string, index int) {
		if paramListDetails.Params[index].DefaultType == nil {
			unassignedParams = append(unassignedParams, paramName)
		}
	})

	if len(unassignedParams) == 0 {
		return
	}

	quoted := make([]string, 0, len(unassignedParams))
	for _, p := range unassignedParams {
		quoted = append(quoted, `"`+p+`"`)
	}
	missingParamNames := strings.Join(quoted, ", ")

	message := localization.LocMessage.ArgMissingForParams().Format(missingParamNames)
	if len(unassignedParams) == 1 {
		message = localization.LocMessage.ArgMissingForParam().Format(missingParamNames)
	}

	e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues, message, node.D.Name, nil)
}

// validateInitSubclassAgainstInitSubclass is the original's `else` arm.
func (e *typeEvaluator) validateInitSubclassAgainstInitSubclass(
	node *parser.ClassNode, classType *ClassType, argList []*Arg,
) {
	// SkipOriginalClass is what makes this look for an *inherited*
	// __init_subclass__: the class being defined does not receive its own.
	initSubclassMethodInfo := e.getTypeOfBoundMember(
		node.D.Name,
		classType,
		"__init_subclass__",
		nil,
		nil,
		MemberAccessFlagsSkipClassMembers|MemberAccessFlagsSkipOriginalClass|
			MemberAccessFlagsSkipAttributeAccessOverride,
		nil,
		0,
	)

	if initSubclassMethodInfo == nil {
		return
	}

	initSubclassMethodType := initSubclassMethodInfo.Type
	if IsNilType(initSubclassMethodType) || initSubclassMethodInfo.ClassType == nil {
		return
	}

	callResult := e.ValidateCallArgs(
		node.D.Name,
		argList,
		&TypeResult{Type: initSubclassMethodType},
		nil,
		false,
		MakeInferenceContext(e.GetNoneType(), false, nil),
	)

	if !callResult.ArgumentErrors {
		return
	}

	diag := e.AddDiagnostic(
		DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.InitSubclassCallFailed(),
		node.D.Name,
		nil,
	)

	initSubclassFunction := initSubclassMethodType
	if IsOverloaded(initSubclassMethodType) {
		overloads := OverloadedTypeGetOverloads(initSubclassMethodType.(*OverloadedType))
		if len(overloads) == 0 {
			return
		}
		initSubclassFunction = overloads[0]
	}

	if !IsFunction(initSubclassFunction) {
		return
	}
	initSubclassDecl := initSubclassFunction.(*FunctionType).Shared.Declaration

	if diag != nil && initSubclassDecl != nil {
		diag.AddRelatedInfo(
			localization.LocAddendum.InitSubclassLocation().Format(
				e.PrintType(ConvertToInstance(initSubclassMethodInfo.ClassType, false), nil)),
			initSubclassDecl.DeclBase().Uri,
			initSubclassDecl.DeclBase().Range,
		)
	}
}

// orderedIndexMap is a minimal insertion-ordered string->int map, standing in
// for the JavaScript Map the original builds and then deletes from while
// iterating the argument list. Order matters because the unassigned-parameter
// diagnostic lists the remaining names.
type orderedIndexMap struct {
	keys   []string
	values map[string]int
}

func newOrderedIndexMap() *orderedIndexMap {
	return &orderedIndexMap{values: map[string]int{}}
}

func (m *orderedIndexMap) Set(key string, value int) {
	if _, exists := m.values[key]; !exists {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

func (m *orderedIndexMap) Get(key string) (int, bool) {
	v, ok := m.values[key]
	return v, ok
}

func (m *orderedIndexMap) Delete(key string) {
	if _, exists := m.values[key]; !exists {
		return
	}
	delete(m.values, key)
	for i, k := range m.keys {
		if k == key {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			break
		}
	}
}

func (m *orderedIndexMap) ForEach(fn func(key string, value int)) {
	for _, k := range m.keys {
		fn(k, m.values[k])
	}
}
