/*
 * typeevaluator_nametail.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412): the
 * adjustments getTypeOfName applies to a symbol's type on the way out.
 *
 * getTypeOfName landed several commits ago with these as recording stubs, which
 * is why they all show identical hit counts on the frontier -- 452 apiece for
 * setSymbolAccessed, addTypeFormForSymbol and reportMissingTypeArgs, 501 apiece
 * for convertSpecialFormToRuntimeValue and reportUseOfTypeCheckOnly. They are
 * hit once per name, which is what makes them worth landing together.
 *
 * setSymbolAccessed is the one with a consequence outside the type: it is what
 * feeds checker.ts's unaccessed-symbol reporting, so until it existed every
 * symbol in the corpus looked unused.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// setSymbolAccessed corresponds to the function of the same name.
func (e *typeEvaluator) setSymbolAccessed(fileInfo *AnalyzerFileInfo, symbol *Symbol, node parser.ParseNode) {
	if !e.IsSpeculativeModeInUse(node) {
		fileInfo.AccessedSymbolSet.Add(symbol.ID)
	}
}

// reportUseOfTypeCheckOnly corresponds to the function of the same name.
func (e *typeEvaluator) reportUseOfTypeCheckOnly(t Type, node parser.ExpressionNode) {
	isTypeCheckingOnly := false
	name := ""

	if IsInstantiableClass(t) && !t.(*ClassType).Priv.IncludeSubclasses {
		isTypeCheckingOnly = ClassTypeIsTypeCheckOnly(t.(*ClassType))
		name = t.(*ClassType).Shared.Name
	} else if IsFunction(t) {
		isTypeCheckingOnly = FunctionTypeIsTypeCheckOnly(t.(*FunctionType))
		name = t.(*FunctionType).Shared.Name
	}

	if isTypeCheckingOnly {
		fileInfo := GetFileInfo(node)

		if !fileInfo.IsStubFile {
			e.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.TypeCheckOnly().Format(name),
				node,
				nil,
			)
		}
	}
}

// convertSpecialFormToRuntimeValue corresponds to the function of the same
// name. The original's convertModule parameter defaults to false.
func (e *typeEvaluator) convertSpecialFormToRuntimeValue(t Type, flags EvalFlags) Type {
	return e.convertSpecialFormToRuntimeValueEx(t, flags, false)
}

func (e *typeEvaluator) convertSpecialFormToRuntimeValueEx(t Type, flags EvalFlags, convertModule bool) Type {
	exemptFlags := EvalFlagsTypeExpression | EvalFlagsInstantiableType | EvalFlagsNoConvertSpecialForm

	if (flags & exemptFlags) != 0 {
		return t
	}

	if convertModule && IsModule(t) && e.prefetched != nil &&
		e.prefetched.ModuleTypeClass != nil && IsInstantiableClass(e.prefetched.ModuleTypeClass) {
		return ClassTypeCloneAsInstance(e.prefetched.ModuleTypeClass.(*ClassType), false)
	}

	// The original's comment: isinstance treats traditional (non-PEP 695) type
	// aliases that are unions as tuples of classes rather than unions.
	if (flags & EvalFlagsIsinstanceArg) != 0 {
		if props := t.Base().Props; IsUnion(t) && props != nil && props.TypeAliasInfo != nil &&
			!props.TypeAliasInfo.Shared.IsTypeAliasType {
			return t
		}
	}

	props := t.Base().Props
	if props == nil || props.SpecialForm == nil {
		return t
	}

	// The original's comment: if this is a type alias and we are not supposed to
	// specialize it, return it as is.
	if (flags&EvalFlagsNoSpecialize) != 0 && props.TypeAliasInfo != nil {
		// The original's comment: special-case TypeAliasType which should be
		// converted in this case.
		if !ClassTypeIsBuiltInNamed(props.SpecialForm, "TypeAliasType") {
			return t
		}
	}

	if props.TypeForm != nil {
		return CloneWithTypeForm(Type(props.SpecialForm), props.TypeForm)
	}

	return props.SpecialForm
}

// isSymbolValidTypeExpression corresponds to the function of the same name.
func (e *typeEvaluator) isSymbolValidTypeExpression(t Type, includesVarDecl bool) bool {
	props := t.Base().Props

	// The original's comment: verify that the name does not refer to a (non type
	// alias) variable.
	if !includesVarDecl || (props != nil && props.TypeAliasInfo != nil) {
		return true
	}

	if IsTypeAliasPlaceholder(t) {
		return true
	}

	if IsTypeVar(t) {
		if props != nil && (props.SpecialForm != nil || props.TypeAliasInfo != nil) {
			return true
		}
	}

	// The original's comment: exempts class types that are created by calling
	// NewType, NamedTuple, etc.
	if IsClass(t) && !t.(*ClassType).Priv.IncludeSubclasses && ClassTypeIsValidTypeAliasClass(t.(*ClassType)) {
		return true
	}

	if e.isSentinelLiteral(t) {
		return true
	}

	return false
}

// addTypeFormForSymbol corresponds to the function of the same name.
func (e *typeEvaluator) addTypeFormForSymbol(
	node parser.ExpressionNode,
	t Type,
	flags EvalFlags,
	includesVarDecl bool,
) Type {
	isIndexBase := false
	if index, ok := node.NodeBase().Parent.(*parser.IndexNode); ok {
		isIndexBase = parser.ParseNode(index.D.LeftExpr) == parser.ParseNode(node)
	}

	if (flags&EvalFlagsTypeFormArg) != 0 && IsTypeVar(t) && TypeVarTypeIsSelf(t.(*TypeVarType)) {
		t = CloneWithTypeForm(t, ConvertToInstance(t, false))
	}

	if (flags&EvalFlagsTypeFormArg) != 0 && IsInstantiableClass(t) && !isIndexBase {
		classType := t.(*ClassType)
		if isTypeFormClass(classType) {
			return e.createTypeFormType(classType, node, nil)
		}

		if rejectedType := e.rejectBareSpecialFormInTypeForm(classType, node); rejectedType != nil {
			return rejectedType
		}

		if ClassTypeIsBuiltInNamed(classType, "Self") {
			t = e.createSelfType(classType, node, nil, flags)
			if IsTypeVar(t) {
				t = CloneWithTypeForm(t, ConvertToInstance(t, false))
			}
		}
	}

	isValid := e.isSymbolValidTypeExpression(t, includesVarDecl)

	// The original's comment: if the type already has type information
	// associated with it, don't replace.
	if props := t.Base().Props; props != nil && props.TypeForm != nil {
		// The original's comment: if the NoConvertSpecialForm flag is set, we are
		// evaluating in the interior of a type expression, so variables are not
		// allowed. Clear any existing type form type for this symbol in this
		// case.
		if (flags&EvalFlagsNoConvertSpecialForm) != 0 && !isValid {
			t = CloneWithTypeForm(t, nil)
		}
		return t
	}

	// The original's comment: if the symbol is not valid for a type expression
	// (e.g. it's a variable), don't add TypeForm info.
	if !isValid {
		return t
	}

	if IsTypeVar(t) && t.(*TypeVarType).Priv.ScopeID != "" && !t.(*TypeVarType).Shared.IsSynthesized {
		if !IsTypeVarTuple(t) || !t.(*TypeVarType).Priv.IsInUnion {
			liveScopeIds := GetTypeVarScopesForNode(node)
			t = CloneWithTypeForm(t, ConvertToInstance(MakeTypeVarsBound(t, liveScopeIds, true), false))
		}
	} else if IsInstantiableClass(t) && !t.(*ClassType).Priv.IncludeSubclasses &&
		!ClassTypeIsSpecialBuiltIn(t.(*ClassType)) {
		if ClassTypeIsBuiltInNamed(t.(*ClassType), "Any") {
			t = CloneWithTypeForm(t, AnyTypeCreate(false))
		} else {
			specialized := SpecializeWithDefaultTypeArgs(t.(*ClassType))
			t = CloneWithTypeForm(t, ClassTypeCloneAsInstance(specialized, false))
		}
	}

	if props := t.Base().Props; props != nil && props.TypeAliasInfo != nil && t.Base().IsInstantiable() {
		typeFormType := t
		if (flags & EvalFlagsNoSpecialize) == 0 {
			typeFormType = e.specializeTypeAliasWithDefaults(typeFormType, nil)
		}

		t = CloneWithTypeForm(t, ConvertToInstance(typeFormType, false))
	}

	return t
}

// ReportMissingTypeArgs corresponds to reportMissingTypeArgs.
func (e *typeEvaluator) ReportMissingTypeArgs(node parser.ExpressionNode, t Type, flags EvalFlags) Type {
	if (flags & EvalFlagsNoSpecialize) != 0 {
		return t
	}

	// The original's comment: is this a generic class that needs to be
	// specialized?
	if IsInstantiableClass(t) {
		classType := t.(*ClassType)

		if (flags&EvalFlagsInstantiableType) != 0 && (flags&EvalFlagsAllowMissingTypeArgs) == 0 {
			props := t.Base().Props
			hasAliasInfo := props != nil && props.TypeAliasInfo != nil
			if !hasAliasInfo && !isTypeFormClass(classType) && RequiresTypeArgs(classType) {
				if classType.Priv.TypeArgs == nil || !boolValue(classType.Priv.IsTypeArgExplicit) {
					// `type.priv.aliasName || type.shared.name` -- aliasName is
					// `string | undefined` and tested for truthiness, so an
					// empty string falls through to the class name too.
					name := classType.Shared.Name
					if classType.Priv.AliasName != nil && *classType.Priv.AliasName != "" {
						name = *classType.Priv.AliasName
					}
					e.AddDiagnostic(
						DiagnosticRuleReportMissingTypeArgument,
						localization.LocMessage.TypeArgsMissingForClass().Format(name),
						node,
						nil,
					)
				}
			}
		}

		if classType.Priv.TypeArgs == nil {
			// The original writes `createSpecializedClassType(...)?.type`, which
			// yields undefined -- a nil Type here -- when the call returns
			// undefined. The nil propagates exactly as it does there.
			if result := e.createSpecializedClassType(classType, nil, flags, node); result != nil {
				t = result.Type
			} else {
				t = nil
			}
		}
	}

	// The original's comment: is this a generic type alias that needs to be
	// specialized?
	if (flags & EvalFlagsInstantiableType) != 0 {
		t = e.specializeTypeAliasWithDefaults(t, node)
	}

	return t
}

/*
 * The four things these reach that are separate units of work.
 */

// createTypeFormType corresponds to the function of the same name.
func (e *typeEvaluator) createTypeFormType(
	classType *ClassType,
	_ parser.ExpressionNode,
	_ []*TypeResultWithNode,
) Type {
	e.unported("createTypeFormType")
	return classType
}

// rejectBareSpecialFormInTypeForm corresponds to the function of the same name.
// It returns nil where the original returns undefined, which is the "not
// rejected" answer.
func (e *typeEvaluator) rejectBareSpecialFormInTypeForm(_ *ClassType, _ parser.ExpressionNode) Type {
	e.unported("rejectBareSpecialFormInTypeForm")
	return nil
}

// createSelfType corresponds to the function of the same name.
func (e *typeEvaluator) createSelfType(
	classType *ClassType,
	_ parser.ExpressionNode,
	_ []*TypeResultWithNode,
	_ EvalFlags,
) Type {
	e.unported("createSelfType")
	return classType
}

// createSpecializedClassType corresponds to the function of the same name. It
// returns nil where the original returns undefined.
func (e *typeEvaluator) createSpecializedClassType(
	classType *ClassType,
	_ []*TypeResultWithNode,
	_ EvalFlags,
	_ parser.ExpressionNode,
) *TypeResult {
	e.unported("createSpecializedClassType")
	return &TypeResult{Type: classType}
}
