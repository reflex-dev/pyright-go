/*
 * dataclasses_synthesize.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/dataClasses.ts (pyright 1.1.412):
 * getInheritedDataClassSlotsNames, isDataClassKeywordOnlySeparator,
 * synthesizeDataClassSlots and synthesizeDataClassMethods.
 *
 * This is the function that turns a decorated class into a dataclass: it walks
 * the class body, decides which names are fields, and writes the synthesized
 * `__init__`, `__new__`, `__replace__`, `__match_args__`, `__hash__`, the
 * comparison operators, `__dataclass_fields__` and `__slots__` into the symbol
 * table. It is also the sole writer of Shared.DataClassEntries and
 * Shared.NamedTupleEntries.
 *
 * Four things in here are subtle enough to be worth stating outright.
 *
 * The two-pass type evaluation is not an optimization. Field *types* are
 * deferred behind closures and evaluated only after the complete entry list has
 * been stored on the class, because a field's annotation may refer back to the
 * class being defined. Evaluating types as fields are discovered would recurse
 * into a class whose entry list is still half-built; evaluating them afterwards
 * means the recursion finds a complete (if not yet accurate) list and
 * terminates.
 *
 * `localDataClassEntries` and `fullDataClassEntries` are different lists on
 * purpose. Only the local list is stored on the class, because each class stores
 * its own contribution and inheritance is recomposed on demand by
 * AddInheritedDataClassEntries. The full list exists to build the constructor
 * signature and to run the "no non-default field after a default field" check
 * against inherited fields as well as local ones.
 *
 * The KW_ONLY separator is a pseudo-field: a `_: KW_ONLY` annotation contributes
 * no parameter, it flips every field after it to keyword-only. That is why
 * detecting it clears variableNameNode rather than continuing.
 *
 * Fields are collected in symbol-table order, which is declaration order, and
 * that order *is* the positional parameter order of `__init__`. Any reordering
 * of the iteration would silently produce a constructor with the arguments in
 * the wrong sequence.
 */

package analyzer

import (
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// getInheritedDataClassSlotsNames corresponds to the function of the same name.
func getInheritedDataClassSlotsNames(classType *ClassType) map[string]bool {
	inheritedSlotsNames := map[string]bool{}
	mro := classType.Shared.Mro
	if len(mro) > 1 {
		mro = mro[1:]
	} else {
		mro = nil
	}
	for _, mroClass := range mro {
		if !IsInstantiableClass(mroClass) {
			continue
		}
		c := mroClass.(*ClassType)
		if c.Shared.SynthesizeDataClassSlotsDeferred != nil {
			c.Shared.SynthesizeDataClassSlotsDeferred()
		}
		for _, name := range c.Shared.LocalSlotsNames {
			inheritedSlotsNames[name] = true
		}
	}
	return inheritedSlotsNames
}

// isDataClassKeywordOnlySeparator corresponds to the function of the same name.
func isDataClassKeywordOnlySeparator(
	evaluator TypeEvaluator, symbol *Symbol, annotationNode parser.ExpressionNode,
) bool {
	if symbol != nil && symbol.IsDataClassKeywordOnly() {
		return true
	}

	annotatedType := evaluator.GetTypeOfAnnotation(annotationNode, &ExpectedTypeOptions{
		VarTypeAnnotation: true,
		AllowFinal:        true,
		AllowClassVar:     true,
	})
	return IsClassInstance(annotatedType) &&
		ClassTypeIsBuiltInNamed(annotatedType.(*ClassType), "KW_ONLY")
}

// SynthesizeDataClassSlots corresponds to synthesizeDataClassSlots. The
// original's comment: calculate generated slots without forcing deferred
// dataclass method synthesis.
func SynthesizeDataClassSlots(evaluator TypeEvaluator, classType *ClassType) {
	classType.Shared.SynthesizeDataClassSlotsDeferred = nil

	if !ClassTypeIsDataClassGenerateSlots(classType) || classType.Shared.LocalSlotsNames != nil {
		return
	}

	inheritedSlotsNames := getInheritedDataClassSlotsNames(classType)
	localSlotsNames := []string{}

	type namedSymbol struct {
		name   string
		symbol *Symbol
	}
	localFields := []namedSymbol{}
	classType.Shared.Fields.ForEach(func(symbol *Symbol, name string) {
		localFields = append(localFields, namedSymbol{name, symbol})
	})

	// The synthesize-methods callback is suppressed for the duration so that
	// reading annotations here cannot re-enter full dataclass synthesis, and it
	// is restored afterwards -- the original does this with try/finally.
	synthesizeMethodsDeferred := classType.Shared.SynthesizeMethodsDeferred
	classType.Shared.SynthesizeMethodsDeferred = nil
	defer func() {
		if synthesizeMethodsDeferred != nil && classType.Shared.SynthesizeMethodsDeferred == nil {
			classType.Shared.SynthesizeMethodsDeferred = synthesizeMethodsDeferred
		}
	}()

	for _, entry := range localFields {
		name, symbol := entry.name, entry.symbol
		if symbol.IsIgnoredForProtocolMatch() || name == "__hash__" {
			continue
		}

		var variableDecl *VariableDeclaration
		for _, decl := range symbol.GetTypedDeclarations() {
			varDecl, ok := decl.(*VariableDeclaration)
			if !ok {
				continue
			}
			container := GetEnclosingClassOrFunction(varDecl.Node)
			if container != nil && container.GetNodeType() == parser.ParseNodeTypeClass {
				variableDecl = varDecl
				break
			}
		}

		if variableDecl == nil {
			for _, decl := range symbol.GetDeclarations() {
				varDecl, ok := decl.(*VariableDeclaration)
				if ok && varDecl.TypeAnnotationNode == nil && varDecl.IsFinal {
					variableDecl = varDecl
					break
				}
			}
		}

		if variableDecl != nil {
			parentNode := variableDecl.Node.NodeBase().Parent
			if parentNode == nil || parentNode.GetNodeType() != parser.ParseNodeTypeTypeAnnotation {
				variableDecl = nil
			}
		}

		isKeywordOnlySeparator := variableDecl != nil && variableDecl.TypeAnnotationNode != nil &&
			isDataClassKeywordOnlySeparator(evaluator, symbol, variableDecl.TypeAnnotationNode)

		if variableDecl != nil && !symbol.IsClassVar() && !symbol.IsInitVar() &&
			!isKeywordOnlySeparator && !inheritedSlotsNames[name] {
			localSlotsNames = append(localSlotsNames, name)
		}
	}

	classType.Shared.LocalSlotsNames = localSlotsNames
	classType.Shared.HasNonEmptySlots = len(localSlotsNames) > 0
}

// dataClassEntryTypeEvaluator pairs an entry with the closure that computes its
// type, matching the original's localEntryTypeEvaluator array.
type dataClassEntryTypeEvaluator struct {
	Entry     *DataClassEntry
	Evaluator func() Type
}

// SynthesizeDataClassMethods corresponds to synthesizeDataClassMethods. The
// original's comment: validates fields for compatibility with a dataclass and
// synthesizes an appropriate __new__ and __init__ methods plus
// __dataclass_fields__ and __match_args__ class variables.
func SynthesizeDataClassMethods(
	evaluator TypeEvaluator,
	node *parser.ClassNode,
	classType *ClassType,
	isNamedTuple bool,
	skipSynthesizeInit bool,
	hasExistingInitMethod bool,
	skipSynthesizeHash bool,
) {
	classTypeVar := SynthesizeTypeVarForSelfCls(classType, true)
	newType := FunctionTypeCreateSynthesizedInstance("__new__", FunctionTypeFlagsConstructorMethod)
	classScopeID := GetTypeVarScopeID(classType)
	newType.Priv.ConstructorTypeVarScopeID = classScopeID
	initType := FunctionTypeCreateSynthesizedInstance("__init__", FunctionTypeFlagsNone)
	initType.Priv.ConstructorTypeVarScopeID = classScopeID

	// The original's comment: generate both a __new__ and an __init__ method.
	// The parameters of the __new__ method are based on field definitions for
	// NamedTuple classes, and the parameters of the __init__ method are based on
	// field definitions in other cases.
	clsName, selfName := "cls", "self"
	FunctionTypeAddParam(newType, FunctionParamCreate(
		parser.ParamCategorySimple, classTypeVar, FunctionParamFlagsTypeDeclared, &clsName, nil, nil))
	if !isNamedTuple {
		FunctionTypeAddDefaultParams(newType, false)
		newType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
	}
	newType.Shared.DeclaredReturnType = ConvertToInstance(classTypeVar, true)

	selfType := SynthesizeTypeVarForSelfCls(classType, false)
	selfParam := FunctionParamCreate(
		parser.ParamCategorySimple, selfType, FunctionParamFlagsTypeDeclared, &selfName, nil, nil)
	FunctionTypeAddParam(initType, selfParam)
	if isNamedTuple {
		FunctionTypeAddDefaultParams(initType, false)
		initType.Shared.Flags |= FunctionTypeFlagsGradualCallableForm
	}
	initType.Shared.DeclaredReturnType = evaluator.GetNoneType()

	// The original's comment: for Python 3.13 and newer, synthesize a __replace__
	// method.
	var replaceType *FunctionType
	if GetFileInfo(node).ExecutionEnvironment.PythonVersion.IsGreaterOrEqualTo(common.PythonVersion3_13) {
		replaceType = FunctionTypeCreateSynthesizedInstance("__replace__", FunctionTypeFlagsNone)
		FunctionTypeAddParam(replaceType, selfParam)
		FunctionTypeAddKeywordOnlyParamSeparator(replaceType)
		replaceType.Shared.DeclaredReturnType = selfType
	}

	// The original's comment: maintain a list of all dataclass entries (including
	// those from inherited classes) plus a list of only those entries added by
	// this class.
	localDataClassEntries := []*DataClassEntry{}
	fullDataClassEntries := []*DataClassEntry{}
	namedTupleEntries := common.NewOrderedSet[string]()
	allAncestorsKnown := AddInheritedDataClassEntries(classType, &fullDataClassEntries)

	if !allAncestorsKnown {
		// The original's comment: if one or more ancestor classes have an unknown
		// type, we cannot safely determine the parameter list, so we'll accept any
		// parameters to avoid a false positive.
		FunctionTypeAddDefaultParams(initType, false)

		if replaceType != nil {
			FunctionTypeAddDefaultParams(replaceType, false)
		}
	}

	// The original's comment: add field-based parameters to either the __new__ or
	// __init__ method based on whether this is a NamedTuple or a dataclass.
	constructorType := initType
	if isNamedTuple {
		constructorType = newType
	}

	localEntryTypeEvaluator := []dataClassEntryTypeEvaluator{}
	sawKeywordOnlySeparator := false

	ClassTypeGetSymbolTable(classType).ForEach(func(symbol *Symbol, name string) {
		if symbol.IsIgnoredForProtocolMatch() {
			return
		}

		// The original's comment: apparently, `__hash__` is special-cased in a
		// dataclass. I can't find this in the spec, but the runtime seems to treat
		// is specially.
		if name == "__hash__" {
			return
		}

		isInferredFinal := false

		// The original's comment: only variables (not functions, classes, etc.)
		// are considered.
		var classVarDecl *VariableDeclaration
		for _, decl := range symbol.GetTypedDeclarations() {
			varDecl, ok := decl.(*VariableDeclaration)
			if !ok {
				continue
			}
			container := GetEnclosingClassOrFunction(varDecl.Node)
			if container == nil || container.GetNodeType() != parser.ParseNodeTypeClass {
				continue
			}
			classVarDecl = varDecl
			break
		}

		// The original's comment: see if this is an unannotated (inferred) Final
		// value.
		if classVarDecl == nil {
			for _, decl := range symbol.GetDeclarations() {
				varDecl, ok := decl.(*VariableDeclaration)
				if ok && varDecl.TypeAnnotationNode == nil && varDecl.IsFinal {
					classVarDecl = varDecl
					break
				}
			}
			isInferredFinal = true
		}

		if classVarDecl == nil {
			// The original's comment: the symbol had no declared type, so it is
			// (mostly) ignored by dataclasses. However, if it is assigned a field
			// descriptor, it will result in a runtime exception.
			declarations := symbol.GetDeclarations()
			if len(declarations) == 0 {
				return
			}
			lastDecl, ok := declarations[len(declarations)-1].(*VariableDeclaration)
			if !ok {
				return
			}

			parentNode := lastDecl.Node.NodeBase().Parent
			if parentNode == nil || parentNode.GetNodeType() != parser.ParseNodeTypeAssignment {
				return
			}
			assignment := parentNode.(*parser.AssignmentNode)

			// The original's comment: if the RHS of the assignment is assigning a
			// field instance where the "init" parameter is set to false, do not
			// include it in the init method.
			if !isNamedTuple && assignment.D.RightExpr.GetNodeType() == parser.ParseNodeTypeCall {
				callType := evaluator.GetTypeOfExpression(
					assignment.D.RightExpr.(*parser.CallNode).D.LeftExpr,
					EvalFlagsCallBaseDefaults, nil).Type

				if isDataclassFieldConstructor(callType, dataClassFieldDescriptorNames(classType)) {
					evaluator.AddDiagnostic(
						DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.DataClassFieldWithoutAnnotation(),
						assignment.D.RightExpr,
						nil,
					)
				}
			}
			return
		}

		// Walk outward from the declaration to the statement that introduced it:
		// either a bare annotation or an annotated assignment.
		var statement parser.ParseNode = classVarDecl.Node
		for statement != nil {
			if statement.GetNodeType() == parser.ParseNodeTypeAssignment {
				break
			}

			if statement.GetNodeType() == parser.ParseNodeTypeTypeAnnotation {
				if p := statement.NodeBase().Parent; p != nil && p.GetNodeType() == parser.ParseNodeTypeAssignment {
					statement = p
				}
				break
			}

			statement = statement.NodeBase().Parent
		}

		if statement == nil {
			return
		}

		var variableNameNode *parser.NameNode
		var typeAnnotationNode *parser.TypeAnnotationNode
		var aliasName *string
		var variableTypeEvaluator func() Type
		hasDefault := false
		isDefaultFactory := false
		isKeywordOnly := ClassTypeIsDataClassKeywordOnly(classType) || sawKeywordOnlySeparator
		var defaultExpr parser.ExpressionNode
		includeInInit := true
		var converter *parser.ArgumentNode

		switch statement.GetNodeType() {
		case parser.ParseNodeTypeAssignment:
			assignment := statement.(*parser.AssignmentNode)

			if assignment.D.LeftExpr.GetNodeType() == parser.ParseNodeTypeTypeAnnotation {
				annotationNode := assignment.D.LeftExpr.(*parser.TypeAnnotationNode)
				if annotationNode.D.ValueExpr.GetNodeType() == parser.ParseNodeTypeName {
					variableNameNode = annotationNode.D.ValueExpr.(*parser.NameNode)
					typeAnnotationNode = annotationNode
					// The closure reads defaultExpr and isInferredFinal at call
					// time, after the loop below may have replaced defaultExpr with
					// the field specifier's `default=` argument. That is the
					// original's behavior and is why these are captured by
					// reference rather than by value.
					variableTypeEvaluator = func() Type {
						if isInferredFinal && defaultExpr != nil {
							return evaluator.GetTypeOfExpression(defaultExpr, EvalFlagsNone, nil).Type
						}

						return evaluator.GetTypeOfAnnotation(annotationNode.D.Annotation, &ExpectedTypeOptions{
							VarTypeAnnotation: true,
							AllowFinal:        !isNamedTuple,
							AllowClassVar:     !isNamedTuple,
						})
					}
				}
			}

			hasDefault = true
			defaultExpr = assignment.D.RightExpr

			// The original's comment: if the RHS of the assignment is assigning a
			// field instance where the "init" parameter is set to false, do not
			// include it in the init method.
			if !isNamedTuple && assignment.D.RightExpr.GetNodeType() == parser.ParseNodeTypeCall {
				callNode := assignment.D.RightExpr.(*parser.CallNode)
				callTypeResult := evaluator.GetTypeOfExpression(
					callNode.D.LeftExpr, EvalFlagsCallBaseDefaults, nil)
				callType := callTypeResult.Type

				if isDataclassFieldConstructor(callType, dataClassFieldDescriptorNames(classType)) {
					fileInfo := GetFileInfo(node)

					if initArg := findKeywordArg(callNode, "init"); initArg != nil && initArg.D.ValueExpr != nil {
						if v, ok := EvaluateStaticBoolExpression(
							initArg.D.ValueExpr, fileInfo.ExecutionEnvironment,
							fileInfo.DefinedConstants, nil, nil); ok {
							includeInInit = v
						}
					} else if r := getDefaultArgValueForFieldSpecifier(
						evaluator, callNode, callTypeResult, "init"); r.Exists {
						includeInInit = r.Value
					}

					if kwOnlyArg := findKeywordArg(callNode, "kw_only"); kwOnlyArg != nil &&
						kwOnlyArg.D.ValueExpr != nil {
						if v, ok := EvaluateStaticBoolExpression(
							kwOnlyArg.D.ValueExpr, fileInfo.ExecutionEnvironment,
							fileInfo.DefinedConstants, nil, nil); ok {
							isKeywordOnly = v
						}
					} else if r := getDefaultArgValueForFieldSpecifier(
						evaluator, callNode, callTypeResult, "kw_only"); r.Exists {
						isKeywordOnly = r.Value
					}

					defaultValueArg := findKeywordArg(callNode, "default")
					hasDefault = defaultValueArg != nil
					if defaultValueArg != nil && defaultValueArg.D.ValueExpr != nil {
						defaultExpr = defaultValueArg.D.ValueExpr
					}

					defaultFactoryArg := findKeywordArg(callNode, "default_factory", "factory")
					if defaultFactoryArg != nil {
						hasDefault = true
						isDefaultFactory = true
						if defaultFactoryArg.D.ValueExpr != nil {
							defaultExpr = defaultFactoryArg.D.ValueExpr
						}
					}

					if aliasArg := findKeywordArg(callNode, "alias"); aliasArg != nil {
						valueType := evaluator.GetTypeOfExpression(
							aliasArg.D.ValueExpr, EvalFlagsNone, nil).Type
						if IsClassInstance(valueType) &&
							ClassTypeIsBuiltInNamed(valueType.(*ClassType), "str") &&
							IsLiteralType(valueType.(*ClassType)) {
							if s, ok := valueType.(*ClassType).Priv.LiteralValue.(LiteralString); ok {
								str := string(s)
								aliasName = &str
							}
						}
					}

					if converterArg := findKeywordArg(callNode, "converter"); converterArg != nil &&
						converterArg.D.ValueExpr != nil {
						converter = converterArg
					}
				}
			}

		case parser.ParseNodeTypeTypeAnnotation:
			annotationStatement := statement.(*parser.TypeAnnotationNode)
			if annotationStatement.D.ValueExpr.GetNodeType() == parser.ParseNodeTypeName {
				variableNameNode = annotationStatement.D.ValueExpr.(*parser.NameNode)
				typeAnnotationNode = annotationStatement
				variableTypeEvaluator = func() Type {
					return evaluator.GetTypeOfAnnotation(
						annotationStatement.D.Annotation, &ExpectedTypeOptions{
							VarTypeAnnotation: true,
							AllowFinal:        !isNamedTuple,
							AllowClassVar:     !isNamedTuple,
						})
				}
			}
		}

		// The original's comment: is this a KW_ONLY separator introduced in Python
		// 3.10? Per the Python docs, the variable name and any assigned value are
		// ignored.
		if variableNameNode != nil && typeAnnotationNode != nil && !isNamedTuple {
			fieldSymbol, _ := classType.Shared.Fields.Get(variableNameNode.D.Value)
			if isDataClassKeywordOnlySeparator(evaluator, fieldSymbol, typeAnnotationNode.D.Annotation) {
				// The original's comment: CPython raises a TypeError if more than
				// one KW_ONLY separator appears within a single dataclass.
				if sawKeywordOnlySeparator {
					evaluator.AddDiagnostic(
						DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.DataClassDuplicateKwOnly().Format(variableNameNode.D.Value),
						variableNameNode,
						nil,
					)
				}

				sawKeywordOnlySeparator = true
				variableNameNode = nil
				typeAnnotationNode = nil
				variableTypeEvaluator = nil
			}
		}

		if variableNameNode == nil || variableTypeEvaluator == nil {
			return
		}

		variableName := variableNameNode.D.Value

		// The original's comment: named tuples don't allow attributes that begin
		// with an underscore.
		if isNamedTuple && len(variableName) > 0 && variableName[0] == '_' {
			evaluator.AddDiagnostic(
				DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.NamedTupleFieldUnderscore(),
				variableNameNode,
				nil,
			)
			return
		}

		// The original's comment: don't include class vars. PEP 557 indicates that
		// they shouldn't be considered data class entries.
		variableSymbol, _ := ClassTypeGetSymbolTable(classType).Get(variableName)
		namedTupleEntries.Add(variableName)

		if variableSymbol != nil && variableSymbol.IsClassVar() {
			// The original's comment: if an ancestor class declared an instance
			// variable but this dataclass declares a ClassVar, delete the older one
			// from the full data class entries.
			if index := findDataClassEntryIndex(fullDataClassEntries, variableName); index >= 0 {
				fullDataClassEntries = append(
					fullDataClassEntries[:index], fullDataClassEntries[index+1:]...)
			}
			localDataClassEntries = append(localDataClassEntries, &DataClassEntry{
				Name:               variableName,
				ClassType:          classType,
				Alias:              aliasName,
				IsKeywordOnly:      false,
				HasDefault:         hasDefault,
				IsDefaultFactory:   isDefaultFactory,
				DefaultExpr:        defaultExpr,
				IncludeInInit:      includeInInit,
				NameNode:           variableNameNode,
				TypeAnnotationNode: typeAnnotationNode,
				Type:               UnknownTypeCreate(false),
				IsClassVar:         true,
				Converter:          converter,
			})
			return
		}

		// The original's comment: create a new data class entry, but defer
		// evaluation of the type until we've compiled the full list of data class
		// entries for this class. This allows us to handle circular references in
		// types.
		dataClassEntry := &DataClassEntry{
			Name:               variableName,
			ClassType:          classType,
			Alias:              aliasName,
			IsKeywordOnly:      isKeywordOnly,
			HasDefault:         hasDefault,
			IsDefaultFactory:   isDefaultFactory,
			DefaultExpr:        defaultExpr,
			IncludeInInit:      includeInInit,
			NameNode:           variableNameNode,
			TypeAnnotationNode: typeAnnotationNode,
			Type:               UnknownTypeCreate(false),
			IsClassVar:         false,
			Converter:          converter,
		}
		localEntryTypeEvaluator = append(localEntryTypeEvaluator,
			dataClassEntryTypeEvaluator{Entry: dataClassEntry, Evaluator: variableTypeEvaluator})

		// The original's comment: add the new entry to the local entry list.
		if insertIndex := findDataClassEntryIndex(localDataClassEntries, variableName); insertIndex >= 0 {
			localDataClassEntries[insertIndex] = dataClassEntry
		} else {
			localDataClassEntries = append(localDataClassEntries, dataClassEntry)
		}

		// The original's comment: add the new entry to the full entry list.
		insertIndex := findDataClassEntryIndex(fullDataClassEntries, variableName)
		if insertIndex >= 0 {
			oldEntry := fullDataClassEntries[insertIndex]

			// The original's comment: while this isn't documented behavior, it
			// appears that the dataclass implementation causes overridden variables
			// to "inherit" default values from parent classes.
			if !dataClassEntry.HasDefault && oldEntry.HasDefault && oldEntry.IncludeInInit {
				dataClassEntry.HasDefault = true
				dataClassEntry.DefaultExpr = oldEntry.DefaultExpr
				hasDefault = true

				// The original's comment: warn the user of this case because it can
				// result in type errors if the default value is incompatible with
				// the new type.
				evaluator.AddDiagnostic(
					DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.DataClassFieldInheritedDefault().Format(variableName),
					variableNameNode,
					nil,
				)
			}

			fullDataClassEntries[insertIndex] = dataClassEntry
		} else {
			fullDataClassEntries = append(fullDataClassEntries, dataClassEntry)
			insertIndex = len(fullDataClassEntries) - 1
		}

		// The original's comment: if we've already seen a entry with a default
		// value defined, all subsequent entries must also have default values.
		if !isKeywordOnly && includeInInit && !skipSynthesizeInit && !hasDefault {
			firstDefaultValueIndex := -1
			for i, p := range fullDataClassEntries {
				if p.HasDefault && p.IncludeInInit && !p.IsKeywordOnly {
					firstDefaultValueIndex = i
					break
				}
			}
			if firstDefaultValueIndex >= 0 && firstDefaultValueIndex < insertIndex {
				evaluator.AddDiagnostic(
					DiagnosticRuleReportGeneralTypeIssues,
					localization.LocMessage.DataClassFieldWithDefault(),
					variableNameNode,
					nil,
				)
			}
		}
	})

	if isNamedTuple {
		classType.Shared.NamedTupleEntries = namedTupleEntries
	} else {
		classType.Shared.DataClassEntries = localDataClassEntries
	}

	// The original's comment: now that the dataClassEntries field has been set
	// with a complete list of local data class entries for this class, perform
	// deferred type evaluations. This could involve circular type dependencies,
	// so it's required that the list be complete (even if types are not yet
	// accurate) before we perform the type evaluations.
	for _, entryEvaluator := range localEntryTypeEvaluator {
		entryEvaluator.Entry.Type = entryEvaluator.Evaluator()
	}

	symbolTable := ClassTypeGetSymbolTable(classType)
	keywordOnlyParams := []FunctionParam{}

	if !skipSynthesizeInit && !hasExistingInitMethod {
		if allAncestorsKnown {
			for _, entry := range fullDataClassEntries {
				if !entry.IncludeInInit {
					continue
				}

				var defaultType Type

				// The original's comment: if the type refers to Self of the parent
				// class, we need to transform it to refer to the Self of this
				// subclass.
				effectiveType := entry.Type
				if entry.ClassType != classType && RequiresSpecialization(effectiveType, nil, 0) {
					solution := NewConstraintSolution(nil)
					AddSolutionForSelfType(solution, entry.ClassType, classType)
					effectiveType = ApplySolvedTypeVars(effectiveType, solution, nil)
				}

				// The original's comment: is the field type a descriptor object? If
				// so, we need to extract the corresponding type of the __init__
				// method parameter from the __set__ method.
				effectiveType = transformDescriptorType(evaluator, effectiveType)

				if entry.Converter != nil {
					fieldType := effectiveType
					effectiveType = getConverterInputType(
						evaluator, entry.Converter, effectiveType, entry.Name)
					symbolTable.Set(entry.Name, getDescriptorForConverterField(
						evaluator, classType, node, entry.NameNode, entry.Converter,
						entry.Name, fieldType, effectiveType))

					if entry.HasDefault {
						defaultType = entry.Type
					}
				} else if entry.HasDefault {
					if entry.IsDefaultFactory || entry.DefaultExpr == nil {
						defaultType = entry.Type
					} else {
						defaultExpr := entry.DefaultExpr
						fileInfo := GetFileInfo(node)
						flags := EvalFlagsNone
						if fileInfo.IsStubFile {
							flags = EvalFlagsConvertEllipsisToAny
						}
						liveTypeVars := GetTypeVarScopesForNode(entry.DefaultExpr)
						boundEffectiveType := MakeTypeVarsBound(effectiveType, liveTypeVars, true)

						// The original's comment: use speculative mode here so we
						// don't cache the results. We'll want to re-evaluate this
						// expression later, potentially with different evaluation
						// flags.
						evaluator.UseSpeculativeMode(defaultExpr, func() {
							defaultType = evaluator.GetTypeOfExpression(
								defaultExpr, flags,
								MakeInferenceContext(boundEffectiveType, false, nil)).Type
						}, nil)

						defaultType = MakeTypeVarsFree(defaultType, liveTypeVars)

						if entry.MroClass != nil && RequiresSpecialization(defaultType, nil, 0) {
							solution := BuildSolutionFromSpecializedClass(entry.MroClass)
							defaultType = ApplySolvedTypeVars(defaultType, solution, nil)
						}
					}
				}

				effectiveName := entry.Name
				if entry.Alias != nil {
					effectiveName = *entry.Alias
				}

				if entry.Alias == nil && entry.NameNode != nil && IsPrivateName(entry.NameNode.D.Value) {
					evaluator.AddDiagnostic(
						DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.DataClassFieldWithPrivateName(),
						entry.NameNode,
						nil,
					)
				}

				param := FunctionParamCreate(
					parser.ParamCategorySimple,
					effectiveType,
					FunctionParamFlagsTypeDeclared,
					&effectiveName,
					defaultType,
					entry.DefaultExpr,
				)

				if entry.IsKeywordOnly {
					keywordOnlyParams = append(keywordOnlyParams, param)
				} else {
					FunctionTypeAddParam(constructorType, param)
				}

				if replaceType != nil {
					// Every __replace__ parameter is optional -- the point of the
					// method is to change a subset of the fields -- so the default
					// is `...` regardless of whether the field has one.
					FunctionTypeAddParam(replaceType, FunctionParamCreate(
						param.Category,
						param.TypeField,
						param.Flags,
						param.Name,
						AnyTypeCreate(true),
						nil,
					))
				}
			}

			if len(keywordOnlyParams) > 0 {
				FunctionTypeAddKeywordOnlyParamSeparator(constructorType)
				for _, param := range keywordOnlyParams {
					FunctionTypeAddParam(constructorType, param)
				}
			}
		}

		symbolTable.Set("__init__", SymbolCreateWithType(SymbolFlagsClassMember, initType, nil))
		symbolTable.Set("__new__", SymbolCreateWithType(SymbolFlagsClassMember, newType, nil))

		if replaceType != nil {
			symbolTable.Set("__replace__", SymbolCreateWithType(SymbolFlagsClassMember, replaceType, nil))
		}
	}

	// The original's comment: synthesize the __match_args__ class variable if it
	// doesn't exist and match_args behavior is not explicitly disabled.
	strType := evaluator.GetBuiltInType(node, "str")
	tupleClassType := evaluator.GetBuiltInType(node, "tuple")
	// The original reads `classType.shared.dataClassBehaviors?.matchArgs ?? true`,
	// so both an absent behaviors object and an absent flag mean true.
	matchArgs := true
	if b := classType.Shared.DataClassBehaviors; b != nil && b.MatchArgs != nil {
		matchArgs = *b.MatchArgs
	}
	if tupleClassType != nil && IsInstantiableClass(tupleClassType) &&
		strType != nil && IsInstantiableClass(strType) &&
		!symbolTable.Has("__match_args__") && matchArgs {
		literalTypes := []*TupleTypeArg{}
		for _, entry := range fullDataClassEntries {
			if entry.IncludeInInit && !entry.IsKeywordOnly {
				// The original's comment: use the field name, not its alias (if it
				// has one).
				literalTypes = append(literalTypes, &TupleTypeArg{
					Type: ClassTypeCloneAsInstance(
						ClassTypeCloneWithLiteral(strType.(*ClassType), LiteralString(entry.Name)), true),
					IsUnbounded: false,
				})
			}
		}
		matchArgsType := ClassTypeCloneAsInstance(
			SpecializeTupleClass(tupleClassType.(*ClassType), literalTypes, true, false), true)
		symbolTable.Set("__match_args__",
			SymbolCreateWithType(SymbolFlagsClassMember, matchArgsType, nil))
	}

	synthesizeComparisonMethod := func(operator string, paramType Type) {
		otherName := "other"
		operatorMethod := FunctionTypeCreateSynthesizedInstance(operator, FunctionTypeFlagsNone)
		FunctionTypeAddParam(operatorMethod, selfParam)
		FunctionTypeAddParam(operatorMethod, FunctionParamCreate(
			parser.ParamCategorySimple, paramType, FunctionParamFlagsTypeDeclared, &otherName, nil, nil))
		operatorMethod.Shared.DeclaredReturnType = evaluator.GetBuiltInObject(node, "bool", nil)
		// The original's comment: if a method of this name already exists, don't
		// override it.
		if existing, _ := symbolTable.Get(operator); existing == nil {
			symbolTable.Set(operator,
				SymbolCreateWithType(SymbolFlagsClassMember, operatorMethod, nil))
		}
	}

	// The original's comment: synthesize comparison operators.
	if !ClassTypeIsDataClassSkipGenerateEq(classType) {
		synthesizeComparisonMethod("__eq__", evaluator.GetBuiltInObject(node, "object", nil))
	}

	if ClassTypeIsDataClassGenerateOrder(classType) {
		for _, operator := range []string{"__lt__", "__le__", "__gt__", "__ge__"} {
			synthesizeComparisonMethod(operator, selfType)
		}
	}

	synthesizeHashFunction := ClassTypeIsDataClassFrozen(classType)
	synthesizeHashNone := !isNamedTuple && !ClassTypeIsDataClassSkipGenerateEq(classType) &&
		!ClassTypeIsDataClassFrozen(classType)

	if skipSynthesizeHash {
		synthesizeHashFunction = false
	}

	// The original's comment: if the user has indicated that a hash function
	// should be generated even if it's unsafe to do so or there is already a hash
	// function present, override the default logic.
	if ClassTypeIsDataClassGenerateHash(classType) {
		synthesizeHashFunction = true
	}

	if synthesizeHashFunction {
		hashMethod := FunctionTypeCreateSynthesizedInstance("__hash__", FunctionTypeFlagsNone)
		FunctionTypeAddParam(hashMethod, selfParam)
		hashMethod.Shared.DeclaredReturnType = evaluator.GetBuiltInObject(node, "int", nil)
		symbolTable.Set("__hash__", SymbolCreateWithType(
			SymbolFlagsClassMember|SymbolFlagsIgnoredForOverrideChecks, hashMethod, nil))
	} else if synthesizeHashNone && !skipSynthesizeHash {
		// A dataclass with __eq__ but no frozen=True is unhashable at runtime,
		// which the type system spells as `__hash__: None`.
		symbolTable.Set("__hash__", SymbolCreateWithType(
			SymbolFlagsClassMember|SymbolFlagsIgnoredForOverrideChecks, evaluator.GetNoneType(), nil))
	}

	dictType := evaluator.GetBuiltInType(node, "dict")
	if IsInstantiableClass(dictType) {
		dictType = ClassTypeCloneAsInstance(ClassTypeSpecialize(
			dictType.(*ClassType),
			[]Type{evaluator.GetBuiltInObject(node, "str", nil), AnyTypeCreate(false)},
			nil, false, nil, nil), true)
	}

	if !isNamedTuple {
		symbolTable.Set("__dataclass_fields__", SymbolCreateWithType(
			SymbolFlagsClassMember|SymbolFlagsClassVar, dictType, nil))
	}

	if ClassTypeIsDataClassGenerateSlots(classType) && classType.Shared.LocalSlotsNames == nil {
		classType.Shared.SynthesizeDataClassSlotsDeferred = nil

		inheritedSlotsNames := getInheritedDataClassSlotsNames(classType)

		localSlotsNames := []string{}
		for _, entry := range localDataClassEntries {
			if entry.IsClassVar {
				continue
			}
			if sym, _ := symbolTable.Get(entry.Name); sym != nil && sym.IsInitVar() {
				continue
			}
			if inheritedSlotsNames[entry.Name] {
				continue
			}
			localSlotsNames = append(localSlotsNames, entry.Name)
		}
		classType.Shared.LocalSlotsNames = localSlotsNames
		classType.Shared.HasNonEmptySlots = len(localSlotsNames) > 0
	}

	// The original's comment: should we synthesize a __slots__ symbol?
	if ClassTypeIsDataClassGenerateSlots(classType) {
		var iterableType Type = UnknownTypeCreate(false)
		if t := evaluator.GetTypingType(node, "Iterable"); t != nil {
			iterableType = t
		}

		if IsInstantiableClass(iterableType) {
			iterableType = ClassTypeCloneAsInstance(ClassTypeSpecialize(
				iterableType.(*ClassType),
				[]Type{evaluator.GetBuiltInObject(node, "str", nil)},
				nil, false, nil, nil), true)
		}

		symbolTable.Set("__slots__", SymbolCreateWithType(
			SymbolFlagsClassMember|SymbolFlagsClassVar, iterableType, nil))
	}

	// The original's comment: if this dataclass derived from a NamedTuple, update
	// the NamedTuple with the specialized entry types.
	entryTypes := make([]Type, 0, len(fullDataClassEntries))
	for _, entry := range fullDataClassEntries {
		entryTypes = append(entryTypes, entry.Type)
	}
	if UpdateNamedTupleBaseClass(classType, entryTypes, true) {
		// The original's comment: recompute the MRO based on the updated NamedTuple
		// base class.
		ComputeMroLinearization(classType)
	}
}

// dataClassFieldDescriptorNames is the original's
// `classType.shared.dataClassBehaviors?.fieldDescriptorNames || []`.
func dataClassFieldDescriptorNames(classType *ClassType) []string {
	if classType.Shared.DataClassBehaviors == nil {
		return nil
	}
	return classType.Shared.DataClassBehaviors.FieldDescriptorNames
}

// findKeywordArg is the original's
// `args.find((arg) => arg.d.name?.d.value === '...')`, generalized to the one
// call site that accepts either of two names.
func findKeywordArg(callNode *parser.CallNode, names ...string) *parser.ArgumentNode {
	for _, arg := range callNode.D.Args {
		if arg.D.Name == nil {
			continue
		}
		for _, name := range names {
			if arg.D.Name.D.Value == name {
				return arg
			}
		}
	}
	return nil
}

// findDataClassEntryIndex is the original's `findIndex((e) => e.name === name)`.
func findDataClassEntryIndex(entries []*DataClassEntry, name string) int {
	for i, e := range entries {
		if e.Name == name {
			return i
		}
	}
	return -1
}
