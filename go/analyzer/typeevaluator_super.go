/*
 * typeevaluator_super.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/typeEvaluator.ts (pyright 1.1.412):
 * getTypeOfSuperCall.
 *
 * `super()` has no single type. What it evaluates to depends on which class the
 * lookup starts *after*, which object it is bound to, and -- crucially -- what
 * is done with the result. The function therefore branches on the parent node:
 * a `super().x` member access can answer precisely, because the member name is
 * known and can be looked up in the MRO past the target class, while a bare
 * `super()` used as a value cannot, and falls back to a documented heuristic.
 *
 * Two independent things are being computed and it is easy to conflate them.
 * The *target class* is where the MRO walk starts; the *bind-to type* is the
 * object the found method binds to. In the zero-argument form the target is the
 * enclosing class and the bind-to type comes from the `self`/`cls` parameter --
 * which is why an explicitly annotated first parameter overrides the implicit
 * one, and why a TypeVar there turns the bind-to type into a conditional type.
 *
 * The protocol carve-out in the member-access branch is for the mixin pattern:
 * when `self` is annotated with a protocol that the target class does not
 * implement, using the target class to constrain the lookup would reject the
 * mixin's own methods. Clearing effectiveTargetClass and setting
 * includeSubclasses is what lets those resolve.
 *
 * `resultIsInstance` is decided separately from everything above and only for
 * the zero/one-argument forms: inside a static or class method there is no
 * instance, so `super()` yields the class rather than an instance of it.
 *
 * The non-member-access branch is explicitly a heuristic and the original says
 * so. It returns the base class following the target in the bind-to type's base
 * list, which is the answer for the common single-inheritance chain and is
 * merely plausible otherwise. Nothing better is available without knowing which
 * member will be accessed.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getTypeOfSuperCall corresponds to the function of the same name.
func (e *typeEvaluator) getTypeOfSuperCall(node *parser.CallNode) *TypeResult {
	if len(node.D.Args) > 2 {
		e.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.SuperCallArgCount(), node.D.Args[2], nil)
	}

	enclosingFunction := GetEnclosingFunctionEvaluationScope(node)
	var enclosingClass *parser.ClassNode
	if enclosingFunction != nil {
		enclosingClass = GetEnclosingClass(enclosingFunction, false)
	}
	var enclosingClassType *ClassType
	if enclosingClass != nil {
		if result := e.GetTypeOfClass(enclosingClass); result != nil {
			enclosingClassType = result.ClassType
		}
	}

	targetClassType := e.superCallTargetClass(node, enclosingFunction, enclosingClassType)

	concreteTargetClassType := e.MakeTopLevelTypeVarsConcrete(targetClassType, false)

	// The original's comment: determine whether to further narrow the type.
	var secondArgType Type
	var bindToType *ClassType

	if len(node.D.Args) > 1 {
		var ok bool
		secondArgType, bindToType, ok = e.superCallBindToFromSecondArg(node, concreteTargetClassType, targetClassType)
		if !ok {
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
	} else if enclosingClassType != nil {
		bindToType = e.superCallImplicitBindTo(node, enclosingClassType)
	}

	// The original's comment: determine whether super() should return an instance
	// of the class or the class itself. It depends on whether the super() call is
	// located within an instance method or not.
	resultIsInstance := true
	if len(node.D.Args) <= 1 {
		if enclosingMethod := GetEnclosingFunction(node); enclosingMethod != nil {
			if methodType := e.GetTypeOfFunction(enclosingMethod); methodType != nil {
				if FunctionTypeIsStaticMethod(methodType.FunctionType) ||
					FunctionTypeIsConstructorMethod(methodType.FunctionType) ||
					FunctionTypeIsClassMethod(methodType.FunctionType) {
					resultIsInstance = false
				}
			}
		}
	}

	// The original's comment: Python docs indicate that super() isn't valid for
	// operations other than member accesses or attribute lookups.
	parentNode := node.NodeBase().Parent
	if parentNode != nil && parentNode.GetNodeType() == parser.ParseNodeTypeMemberAccess {
		return e.superCallForMemberAccess(
			parentNode.(*parser.MemberAccessNode), concreteTargetClassType,
			bindToType, secondArgType, resultIsInstance)
	}

	// The original's comment: handle the super() call when used outside of a
	// member access expression.
	return e.superCallOutsideMemberAccess(concreteTargetClassType, bindToType, resultIsInstance)
}

// superCallTargetClass is the original's block that determines which class the
// "super" call is applied to. The original's comment: if there is no first
// argument, then the class is implicit.
func (e *typeEvaluator) superCallTargetClass(
	node *parser.CallNode, enclosingFunction parser.ParseNode, enclosingClassType *ClassType,
) Type {
	if len(node.D.Args) > 0 {
		targetClassType := e.GetTypeOfExpression(node.D.Args[0].D.ValueExpr, EvalFlagsNone, nil).Type
		concreteTargetClassType := e.MakeTopLevelTypeVarsConcrete(targetClassType, false)

		if !IsAnyOrUnknown(concreteTargetClassType) && !IsInstantiableClass(concreteTargetClassType) &&
			!IsMetaclassInstance(concreteTargetClassType) {
			e.AddDiagnostic(DiagnosticRuleReportArgumentType,
				localization.LocMessage.SuperCallFirstArg().Format(e.PrintType(targetClassType, nil)),
				node.D.Args[0].D.ValueExpr, nil)
		}

		return targetClassType
	}

	if enclosingClassType == nil {
		e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
			localization.LocMessage.SuperCallZeroArgForm(), node.D.LeftExpr, nil)
		return UnknownTypeCreate(false)
	}

	// The original's comment: zero-argument forms of super are not allowed within
	// static methods. This results in a runtime exception.
	if enclosingFunction != nil {
		if fnNode, ok := enclosingFunction.(*parser.FunctionNode); ok {
			functionInfo := GetFunctionInfoFromDecorators(e, fnNode, true)
			if functionInfo != nil && (functionInfo.Flags&FunctionTypeFlagsStaticMethod) != 0 {
				e.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.SuperCallZeroArgFormStaticMethod(), node.D.LeftExpr, nil)
			}
		}
	}

	return enclosingClassType
}

// superCallBindToFromSecondArg is the original's `if (node.d.args.length > 1)`
// arm. The third result is false when the original returns early with Unknown.
func (e *typeEvaluator) superCallBindToFromSecondArg(
	node *parser.CallNode, concreteTargetClassType Type, targetClassType Type,
) (Type, *ClassType, bool) {
	secondArgType := e.GetTypeOfExpression(node.D.Args[1].D.ValueExpr, EvalFlagsNone, nil).Type
	secondArgConcreteType := e.MakeTopLevelTypeVarsConcrete(secondArgType, false)

	reportError := false
	var bindToType *ClassType

	DoForEachSubtype(secondArgConcreteType, func(secondArgSubtype Type, _ int, _ []Type) {
		switch {
		case IsAnyOrUnknown(secondArgSubtype):
			// The original's comment: ignore unknown or any types.

		case IsClassInstance(secondArgSubtype):
			if IsInstantiableClass(concreteTargetClassType) {
				if !DerivesFromClassRecursive(
					ClassTypeCloneAsInstantiable(secondArgSubtype.(*ClassType), false),
					concreteTargetClassType.(*ClassType), true) {
					reportError = true
				}
			}
			bindToType = secondArgSubtype.(*ClassType)

		case IsInstantiableClass(secondArgSubtype):
			if IsInstantiableClass(concreteTargetClassType) {
				// `super(type, cls)` is exempt: every class derives from `type`
				// as a metaclass, so the derivation check does not apply.
				if !ClassTypeIsBuiltInNamed(concreteTargetClassType.(*ClassType), "type") &&
					!DerivesFromClassRecursive(secondArgSubtype.(*ClassType),
						concreteTargetClassType.(*ClassType), true) {
					reportError = true
				}
			}
			bindToType = secondArgSubtype.(*ClassType)

		default:
			reportError = true
		}
	})

	if reportError {
		e.AddDiagnostic(DiagnosticRuleReportArgumentType,
			localization.LocMessage.SuperCallSecondArg().Format(e.PrintType(targetClassType, nil)),
			node.D.Args[1].D.ValueExpr, nil)
		return nil, nil, false
	}

	return secondArgType, bindToType, true
}

// superCallImplicitBindTo is the original's `else if (enclosingClassType)` arm:
// the zero-argument form derives its bind-to type from the enclosing method's
// first parameter when that parameter is explicitly annotated.
func (e *typeEvaluator) superCallImplicitBindTo(
	node *parser.CallNode, enclosingClassType *ClassType,
) *ClassType {
	bindToType := ClassTypeCloneAsInstance(enclosingClassType, false)

	// The original's comment: get the type from the self or cls parameter if it
	// is explicitly annotated. If it's a TypeVar, change the bindToType into a
	// conditional type.
	enclosingMethod := GetEnclosingFunction(node)
	var implicitBindToType Type

	if enclosingMethod != nil {
		if methodTypeInfo := e.GetTypeOfFunction(enclosingMethod); methodTypeInfo != nil {
			methodType := methodTypeInfo.FunctionType
			if FunctionTypeIsClassMethod(methodType) || FunctionTypeIsConstructorMethod(methodType) ||
				FunctionTypeIsInstanceMethod(methodType) {
				if len(methodType.Shared.Parameters) > 0 &&
					FunctionParamIsTypeDeclared(methodType.Shared.Parameters[0]) {
					paramType := FunctionTypeGetParamType(methodType, 0)
					liveScopeIds := GetTypeVarScopesForNode(node)
					paramType = MakeTypeVarsBound(paramType, liveScopeIds, true)
					implicitBindToType = e.MakeTopLevelTypeVarsConcrete(paramType, false)
				}
			}
		}
	}

	if bindToType != nil && !IsNilType(implicitBindToType) {
		if typeCondition := GetTypeCondition(implicitBindToType); typeCondition != nil {
			if conditioned, ok := AddConditionToType(bindToType, typeCondition, nil).(*ClassType); ok {
				bindToType = conditioned
			}
		} else if IsClass(implicitBindToType) {
			bindToType = implicitBindToType.(*ClassType)
		}
	}

	return bindToType
}

// superCallForMemberAccess is the original's
// `if (parentNode?.nodeType === ParseNodeType.MemberAccess)` arm.
func (e *typeEvaluator) superCallForMemberAccess(
	parentNode *parser.MemberAccessNode,
	concreteTargetClassType Type,
	bindToType *ClassType,
	secondArgType Type,
	resultIsInstance bool,
) *TypeResult {
	memberName := parentNode.D.Member.D.Value
	var effectiveTargetClass *ClassType
	if IsClass(concreteTargetClassType) {
		effectiveTargetClass = concreteTargetClassType.(*ClassType)
	}

	// The original's comment: if the bind-to type is a protocol, don't use the
	// effective target class. This pattern is used for mixins, where the mixin
	// type is a protocol class that is used to decorate the "self" or "cls"
	// parameter.
	isProtocolClass := false
	if bindToType != nil && ClassTypeIsProtocolClass(bindToType) && effectiveTargetClass != nil {
		comparand := bindToType
		if bindToType.Base().IsInstance() {
			comparand = ClassTypeCloneAsInstantiable(bindToType, false)
		}
		if !ClassTypeIsSameGenericClass(comparand, effectiveTargetClass, 0) {
			isProtocolClass = true
			effectiveTargetClass = nil
		}
	}

	if bindToType != nil {
		bindToType = SelfSpecializeClass(bindToType, &SelfSpecializeOptions{UseBoundTypeVars: true})
	}

	var lookupResults *ClassMember
	if bindToType != nil {
		lookupResults = LookUpClassMember(bindToType, memberName, MemberAccessFlagsDefault, effectiveTargetClass)
	}

	var resultType Type
	switch {
	case lookupResults != nil && IsInstantiableClass(lookupResults.ClassType):
		resultType = lookupResults.ClassType

		if isProtocolClass {
			// The original's comment: if the bindToType is a protocol class, set
			// the "include subclasses" flag so we don't enforce that called
			// methods are implemented within the protocol.
			resultType = ClassTypeCloneIncludeSubclasses(resultType.(*ClassType), true)
		}

	case effectiveTargetClass != nil && !IsAnyOrUnknown(effectiveTargetClass) &&
		!DerivesFromAnyOrUnknown(effectiveTargetClass):
		resultType = UnknownTypeCreate(false)
		if e.prefetched != nil && e.prefetched.ObjectClass != nil {
			resultType = e.prefetched.ObjectClass
		}

	default:
		resultType = UnknownTypeCreate(false)
	}

	var bindToSelfType Type
	if bindToType != nil {
		if !IsNilType(secondArgType) {
			// The original's comment: if a TypeVar was passed as the second
			// argument, use it to derive the the self type.
			if IsTypeVar(secondArgType) {
				bindToSelfType = ConvertToInstance(secondArgType, true)
			}
		} else {
			// The original's comment: if this is a zero-argument form of super(),
			// synthesize a Self type to bind to.
			bindToSelfType = CloneForCondition(
				TypeVarTypeCloneAsBound(SynthesizeTypeVarForSelfCls(
					ClassTypeCloneIncludeSubclasses(bindToType, false), false)),
				bindToTypeCondition(bindToType))
		}
	}

	var resultingType Type = resultType
	if resultIsInstance {
		resultingType = ConvertToInstance(resultType, false)
	}

	return &TypeResult{Type: resultingType, BindToSelfType: bindToSelfType}
}

// superCallOutsideMemberAccess is the original's trailing heuristic; see the
// file header for why it is one.
func (e *typeEvaluator) superCallOutsideMemberAccess(
	concreteTargetClassType Type, bindToType *ClassType, resultIsInstance bool,
) *TypeResult {
	if !IsInstantiableClass(concreteTargetClassType) {
		return &TypeResult{Type: UnknownTypeCreate(false)}
	}
	targetClass := concreteTargetClassType.(*ClassType)

	// The original's comment: we don't know which member is going to be accessed,
	// so we cannot deterministically determine the correct type in this case.
	// We'll use a heuristic that produces the "correct" (desired) behavior in most
	// cases. If there's a bindToType and the targetClassType is one of the base
	// classes of the bindToType, we'll return the next base class.
	if bindToType != nil {
		var nextBaseClassType Type

		comparand := bindToType
		if bindToType.Base().IsInstance() {
			comparand = ClassTypeCloneAsInstantiable(bindToType, false)
		}

		if ClassTypeIsSameGenericClass(comparand, targetClass, 0) {
			if len(bindToType.Shared.BaseClasses) > 0 {
				nextBaseClassType = bindToType.Shared.BaseClasses[0]
			}
		} else {
			baseClassIndex := -1
			for i, baseClass := range bindToType.Shared.BaseClasses {
				if IsClass(baseClass) && ClassTypeIsSameGenericClass(baseClass.(*ClassType), targetClass, 0) {
					baseClassIndex = i
					break
				}
			}

			if baseClassIndex >= 0 && baseClassIndex < len(bindToType.Shared.BaseClasses)-1 {
				nextBaseClassType = bindToType.Shared.BaseClasses[baseClassIndex+1]
			}
		}

		if nextBaseClassType != nil {
			if IsInstantiableClass(nextBaseClassType) {
				nextBaseClassType = SpecializeForBaseClass(bindToType, nextBaseClassType.(*ClassType))
			}
			if resultIsInstance {
				return &TypeResult{Type: ConvertToInstance(nextBaseClassType, true)}
			}
			return &TypeResult{Type: nextBaseClassType}
		}

		// The original's comment: there's not much we can say about the type.
		// Simply return object or type.
		if e.prefetched != nil && e.prefetched.TypeClass != nil &&
			IsInstantiableClass(e.prefetched.TypeClass) {
			if resultIsInstance {
				return &TypeResult{Type: e.GetObjectType()}
			}
			return &TypeResult{Type: ConvertToInstance(e.prefetched.TypeClass, true)}
		}

		return &TypeResult{Type: UnknownTypeCreate(false)}
	}

	// The original's comment: if the class derives from one or more unknown
	// classes, return unknown here to prevent spurious errors.
	for _, mroBase := range targetClass.Shared.Mro {
		if IsAnyOrUnknown(mroBase) {
			return &TypeResult{Type: UnknownTypeCreate(false)}
		}
	}

	baseClasses := targetClass.Shared.BaseClasses
	if len(baseClasses) > 0 {
		if baseClassType := baseClasses[0]; IsInstantiableClass(baseClassType) {
			if resultIsInstance {
				return &TypeResult{Type: ClassTypeCloneAsInstance(baseClassType.(*ClassType), false)}
			}
			return &TypeResult{Type: baseClassType}
		}
	}

	return &TypeResult{Type: UnknownTypeCreate(false)}
}

// bindToTypeCondition is the original's `bindToType.props?.condition`.
func bindToTypeCondition(t *ClassType) []TypeCondition {
	if t.Base().Props == nil {
		return nil
	}
	return t.Base().Props.Condition
}
