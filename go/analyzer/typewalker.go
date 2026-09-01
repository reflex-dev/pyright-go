/*
 * typewalker.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 * Author: Eric Traut
 *
 * A type that walks the parts of a type (e.g. the parameters of a function or
 * the type arguments of a class). It detects and prevents infinite recursion.
 *
 * Transliterated from analyzer/typeWalker.ts (pyright 1.1.412).
 *
 * The TypeScript class is meant to be subclassed, with subclasses overriding
 * the visitX methods and calling back into walk(). Go has no method overriding,
 * so TypeWalker carries a `self` pointing at the concrete visitor, set by
 * NewTypeWalker. A subclass embeds *TypeWalker, implements TypeWalkerVisitor,
 * and passes itself in -- the same virtual-dispatch stand-in used by
 * common/uri/baseuri.go.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
)

// TypeWalkerVisitor is the set of methods a TypeWalker subclass may override.
// TypeWalkerDefaults supplies do-nothing implementations of all of them, so an
// embedder only writes the ones it cares about.
type TypeWalkerVisitor interface {
	VisitTypeAlias(t Type)
	VisitUnbound(t *UnboundType)
	VisitAny(t *AnyType)
	VisitUnknown(t *UnknownType)
	VisitNever(t *NeverType)
	VisitFunction(t *FunctionType)
	VisitOverloaded(t *OverloadedType)
	VisitClass(t *ClassType)
	VisitModule(t *ModuleType)
	VisitUnion(t *UnionType)
	VisitTypeVar(t *TypeVarType)
}

// TypeWalker corresponds to the class of the same name.
type TypeWalker struct {
	self TypeWalkerVisitor

	recursionCount    int
	isWalkCanceled    bool
	hitRecursionLimit bool
}

// NewTypeWalker returns a walker that dispatches to self. Pass the outermost
// embedder, not the embedded *TypeWalker.
func NewTypeWalker(self TypeWalkerVisitor) *TypeWalker {
	return &TypeWalker{self: self}
}

// IsRecursionLimitHit corresponds to the isRecursionLimitHit getter.
func (w *TypeWalker) IsRecursionLimitHit() bool { return w.hitRecursionLimit }

// IsWalkCanceled corresponds to the isWalkCanceled getter.
func (w *TypeWalker) IsWalkCanceled() bool { return w.isWalkCanceled }

// Walk corresponds to TypeWalker.walk.
func (w *TypeWalker) Walk(t Type) {
	if w.recursionCount > MaxTypeRecursionCount {
		w.hitRecursionLimit = true
		return
	}

	if w.isWalkCanceled {
		return
	}

	w.recursionCount++

	if t.Base().Props != nil && t.Base().Props.TypeAliasInfo != nil {
		w.self.VisitTypeAlias(t)
	}

	switch t.Base().Category {
	case TypeCategoryUnbound:
		w.self.VisitUnbound(t.(*UnboundType))

	case TypeCategoryAny:
		w.self.VisitAny(t.(*AnyType))

	case TypeCategoryUnknown:
		w.self.VisitUnknown(t.(*UnknownType))

	case TypeCategoryNever:
		w.self.VisitNever(t.(*NeverType))

	case TypeCategoryFunction:
		w.self.VisitFunction(t.(*FunctionType))

	case TypeCategoryOverloaded:
		w.self.VisitOverloaded(t.(*OverloadedType))

	case TypeCategoryClass:
		w.self.VisitClass(t.(*ClassType))

	case TypeCategoryModule:
		w.self.VisitModule(t.(*ModuleType))

	case TypeCategoryUnion:
		w.self.VisitUnion(t.(*UnionType))

	case TypeCategoryTypeVar:
		w.self.VisitTypeVar(t.(*TypeVarType))

	default:
		common.AssertNever(t.Base().Category, "")
	}

	w.recursionCount--
}

// CancelWalk corresponds to TypeWalker.cancelWalk.
func (w *TypeWalker) CancelWalk() {
	w.isWalkCanceled = true
}

// TypeWalkerDefaults provides the base-class visit implementations. Embed both
// it and *TypeWalker in a subclass, then override only the interesting ones.
//
// It has to hold the walker to reproduce the base class's recursive visits, so
// construct it with NewTypeWalkerDefaults.
type TypeWalkerDefaults struct {
	walker *TypeWalker
}

// VisitTypeAlias corresponds to TypeWalker.visitTypeAlias.
func (d *TypeWalkerDefaults) VisitTypeAlias(t Type) {
	aliasInfo := t.Base().Props.TypeAliasInfo
	assert(aliasInfo != nil, "")

	if aliasInfo.TypeArgs != nil {
		for _, typeArg := range aliasInfo.TypeArgs {
			d.walker.Walk(typeArg)
			if d.walker.isWalkCanceled {
				break
			}
		}
	}
}

// VisitUnbound has nothing to do.
func (d *TypeWalkerDefaults) VisitUnbound(t *UnboundType) {}

// VisitAny has nothing to do.
func (d *TypeWalkerDefaults) VisitAny(t *AnyType) {}

// VisitUnknown has nothing to do.
func (d *TypeWalkerDefaults) VisitUnknown(t *UnknownType) {}

// VisitNever has nothing to do.
func (d *TypeWalkerDefaults) VisitNever(t *NeverType) {}

// VisitFunction corresponds to TypeWalker.visitFunction.
func (d *TypeWalkerDefaults) VisitFunction(t *FunctionType) {
	for i := range t.Shared.Parameters {
		// Ignore parameters such as "*" that have no name.
		if t.Shared.Parameters[i].Name != nil && *t.Shared.Parameters[i].Name != "" {
			paramType := FunctionTypeGetParamType(t, i)
			d.walker.Walk(paramType)
			if d.walker.isWalkCanceled {
				break
			}
		}
	}

	if !d.walker.isWalkCanceled && !FunctionTypeIsParamSpecValue(t) {
		returnType := t.Shared.DeclaredReturnType
		if returnType == nil && t.Shared.InferredReturnType != nil {
			returnType = t.Shared.InferredReturnType.Type
		}
		if returnType != nil {
			d.walker.Walk(returnType)
		}
	}
}

// VisitOverloaded corresponds to TypeWalker.visitOverloaded.
func (d *TypeWalkerDefaults) VisitOverloaded(t *OverloadedType) {
	overloads := OverloadedTypeGetOverloads(t)
	for _, overload := range overloads {
		d.walker.Walk(overload)
		if d.walker.isWalkCanceled {
			break
		}
	}

	impl := OverloadedTypeGetImplementation(t)
	if impl != nil {
		d.walker.Walk(impl)
	}
}

// VisitClass corresponds to TypeWalker.visitClass.
func (d *TypeWalkerDefaults) VisitClass(t *ClassType) {
	if !ClassTypeIsPseudoGenericClass(t) {
		var typeArgs []Type
		if t.Priv.TupleTypeArgs != nil {
			typeArgs = make([]Type, 0, len(t.Priv.TupleTypeArgs))
			for _, arg := range t.Priv.TupleTypeArgs {
				typeArgs = append(typeArgs, arg.Type)
			}
		} else {
			typeArgs = t.Priv.TypeArgs
		}

		if typeArgs != nil {
			for _, argType := range typeArgs {
				d.walker.Walk(argType)
				if d.walker.isWalkCanceled {
					break
				}
			}
		}
	}
}

// VisitModule has nothing to do.
func (d *TypeWalkerDefaults) VisitModule(t *ModuleType) {}

// VisitUnion corresponds to TypeWalker.visitUnion.
func (d *TypeWalkerDefaults) VisitUnion(t *UnionType) {
	for _, subtype := range t.Priv.Subtypes {
		d.walker.Walk(subtype)
		if d.walker.isWalkCanceled {
			break
		}
	}
}

// VisitTypeVar has nothing to do.
func (d *TypeWalkerDefaults) VisitTypeVar(t *TypeVarType) {}

// NewTypeWalkerDefaults wires the default visits back to the walker that
// dispatches to them.
func NewTypeWalkerDefaults(walker *TypeWalker) *TypeWalkerDefaults {
	return &TypeWalkerDefaults{walker: walker}
}
