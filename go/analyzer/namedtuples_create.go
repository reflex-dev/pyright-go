/*
 * namedtuples_create.go
 * Copyright (c) Microsoft Corporation.
 * Licensed under the MIT license.
 *
 * Transliterated from analyzer/namedTuples.ts (pyright 1.1.412):
 * createNamedTupleType, renameKeyword and renameUnderscore.
 *
 * This handles the *functional* NamedTuple forms -- `collections.namedtuple`
 * and `typing.NamedTuple("N", [...])` -- where the fields are given as runtime
 * values rather than as class-body annotations. There is no class statement to
 * bind, so the whole class is synthesized from the call arguments.
 *
 * `includesTypes` distinguishes the two spellings and changes more than it
 * looks. `typing.NamedTuple` takes (name, type) pairs and its fields are
 * type-declared; `collections.namedtuple` takes bare names, so every field is
 * Unknown and the parameters are *not* marked TypeDeclared -- which is what
 * keeps strict mode from reporting them as partially-unknown. `rename=` is
 * likewise accepted only in the untyped form, because only that one has it.
 *
 * The three ways a field list can be written are handled separately and the
 * middle one is easy to overlook: a single space/comma-delimited *string*
 * ("x y z"), a list or tuple of names, or anything else. That third case is the
 * important one: if the field list is a dynamic expression, nothing about the
 * shape is knowable, so the code sets addGenericGetAttribute and the class gets
 * a permissive `__getattribute__` returning Any plus a `__new__` that accepts
 * anything. That is the difference between "we could not analyze this" and a
 * cascade of false attribute errors.
 *
 * Defaults are positional and right-aligned: `defaults=(1, 2)` on a five-field
 * namedtuple defaults the last two. That is what firstParamWithDefaultIndex
 * computes. When the defaults argument is a dynamic expression its length is
 * unknown, and the code deliberately defaults *every* field rather than none,
 * so an under-supplied constructor call is not falsely reported.
 *
 * Two setTypeResultForNode calls near the end are not caching conveniences. The
 * entry list and the entries argument are written into the type cache so the
 * checker does not walk back into them later and re-report the string literals
 * as unanalyzable.
 */

package analyzer

import (
	"strconv"
	"strings"

	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/localization"
	"github.com/microsoft/pyright/go/parser"
)

// CreateNamedTupleType corresponds to createNamedTupleType.
func CreateNamedTupleType(
	evaluator TypeEvaluator,
	errorNode parser.ExpressionNode,
	argList []*Arg,
	includesTypes bool,
) *ClassType {
	fileInfo := GetFileInfo(errorNode)
	className := "namedtuple"
	namedTupleEntries := common.NewOrderedSet[string]()

	// The original's comment: the "rename" parameter is supported only in the
	// untyped version.
	allowRename := false
	if !includesTypes {
		if renameArg := findNamedTupleArg(argList, true, "rename"); renameArg != nil &&
			renameArg.ValueExpression != nil {
			if v, known := EvaluateStaticBoolExpression(renameArg.ValueExpression,
				fileInfo.ExecutionEnvironment, fileInfo.DefinedConstants, nil, nil); known && v {
				allowRename = true
			}
		}
	}

	if len(argList) == 0 {
		evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.NamedTupleFirstArg(), errorNode, nil)
	} else {
		nameArg := argList[0]
		if nameArg.ArgCategory != parser.ArgCategorySimple {
			var node parser.ExpressionNode = errorNode
			if argList[0].ValueExpression != nil {
				node = argList[0].ValueExpression
			}
			evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType,
				localization.LocMessage.NamedTupleFirstArg(), node, nil)
		} else if nameArg.ValueExpression != nil &&
			nameArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeStringList {
			className = joinStringListValue(nameArg.ValueExpression.(*parser.StringListNode))
		}
	}

	// The original's comment: is there is a default arg? If so, is it defined in
	// a way that we can determine its length statically?
	defaultsArg := findNamedTupleArg(argList, false, "defaults")
	defaultArgCount := 0
	defaultArgCountKnown := true
	if defaultsArg != nil && defaultsArg.ValueExpression != nil {
		defaultsArgType := evaluator.GetTypeOfExpression(defaultsArg.ValueExpression, EvalFlagsNone, nil).Type
		if IsClassInstance(defaultsArgType) && IsTupleClass(defaultsArgType.(*ClassType)) &&
			!IsUnboundedTupleClass(defaultsArgType.(*ClassType)) &&
			defaultsArgType.(*ClassType).Priv.TupleTypeArgs != nil {
			defaultArgCount = len(defaultsArgType.(*ClassType).Priv.TupleTypeArgs)
		} else {
			defaultArgCountKnown = false
		}
	}

	var namedTupleType Type = UnknownTypeCreate(false)
	if t := evaluator.GetTypingType(errorNode, "NamedTuple"); t != nil {
		namedTupleType = t
	}

	var effectiveMetaclass Type = UnknownTypeCreate(false)
	if IsInstantiableClass(namedTupleType) {
		effectiveMetaclass = namedTupleType.(*ClassType).Shared.EffectiveMetaclass
	}

	classType := ClassTypeCreateInstantiable(
		className,
		GetClassFullName(errorNode, fileInfo.ModuleName, className),
		fileInfo.ModuleName,
		fileInfo.FileUri,
		ClassTypeFlagsValidTypeAliasClass,
		GetTypeSourceID(errorNode),
		nil,
		effectiveMetaclass,
		nil,
	)
	classType.Shared.BaseClasses = append(classType.Shared.BaseClasses, namedTupleType)
	classType.Shared.TypeVarScopeID = TypeVarScopeId(GetScopeIdForNode(errorNode))

	classFields := ClassTypeGetSymbolTable(classType)
	classFields.Set("__class__", SymbolCreateWithType(
		SymbolFlagsClassMember|SymbolFlagsIgnoredForProtocolMatch, classType, nil))

	clsName, selfName, nameName := "cls", "self", "name"

	classTypeVar := SynthesizeTypeVarForSelfCls(classType, true)
	constructorType := FunctionTypeCreateSynthesizedInstance("__new__", FunctionTypeFlagsConstructorMethod)
	constructorType.Shared.DeclaredReturnType = ConvertToInstance(classTypeVar, true)
	constructorType.Priv.ConstructorTypeVarScopeID = GetTypeVarScopeID(classType)
	if IsAssignmentToDefaultsFollowingNamedTuple(errorNode) {
		// `N.__new__.__defaults__ = (...)` is the runtime idiom for adding
		// defaults after the fact; the declared defaults cannot be checked
		// against it.
		constructorType.Shared.Flags |= FunctionTypeFlagsDisableDefaultChecks
	}
	constructorType.Shared.TypeVarScopeID = classType.Shared.TypeVarScopeID
	FunctionTypeAddParam(constructorType, FunctionParamCreate(
		parser.ParamCategorySimple, classTypeVar, FunctionParamFlagsTypeDeclared, &clsName, nil, nil))

	matchArgsNames := []string{}

	selfParam := FunctionParamCreate(
		parser.ParamCategorySimple,
		SynthesizeTypeVarForSelfCls(classType, false),
		FunctionParamFlagsTypeDeclared,
		&selfName, nil, nil)

	addGenericGetAttribute := false
	entryTypes := []Type{}

	if len(argList) < 2 {
		evaluator.AddDiagnostic(DiagnosticRuleReportCallIssue,
			localization.LocMessage.NamedTupleSecondArg(), errorNode, nil)
		addGenericGetAttribute = true
	} else {
		b := &namedTupleBuilder{
			evaluator:            evaluator,
			fileInfo:             fileInfo,
			classFields:          classFields,
			constructorType:      constructorType,
			namedTupleEntries:    namedTupleEntries,
			allowRename:          allowRename,
			includesTypes:        includesTypes,
			defaultArgCount:      defaultArgCount,
			defaultArgCountKnown: defaultArgCountKnown,
		}

		entriesArg := argList[1]
		if entriesArg.ArgCategory != parser.ArgCategorySimple {
			addGenericGetAttribute = true
		} else {
			switch {
			case !includesTypes && entriesArg.ValueExpression != nil &&
				entriesArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeStringList:
				b.addEntriesFromNameString(entriesArg.ValueExpression.(*parser.StringListNode))

			case entriesArg.ValueExpression != nil &&
				(entriesArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeList ||
					entriesArg.ValueExpression.GetNodeType() == parser.ParseNodeTypeTuple):
				b.addEntriesFromSequence(entriesArg.ValueExpression)

			default:
				// The original's comment: a dynamic expression was used, so we
				// can't evaluate the named tuple statically.
				b.addGenericGetAttribute = true
			}

			addGenericGetAttribute = b.addGenericGetAttribute
			matchArgsNames = b.matchArgsNames
			entryTypes = b.entryTypes

			if entriesArg.ValueExpression != nil && !addGenericGetAttribute {
				// The original's comment: set the type of the value expression node
				// to Any so we don't attempt to re-evaluate it later, potentially
				// generating "partially unknown" errors in strict mode.
				evaluator.SetTypeResultForNode(entriesArg.ValueExpression,
					&TypeResult{Type: AnyTypeCreate(false)}, EvalFlagsNone)
			}
		}
	}

	classType.Shared.NamedTupleEntries = namedTupleEntries

	if addGenericGetAttribute {
		// Nothing about the shape is knowable, so the constructor accepts
		// anything and the tuple base becomes homogeneous rather than fixed.
		constructorType.Shared.Parameters = nil
		FunctionTypeAddDefaultParams(constructorType, false)
		entryTypes = append(entryTypes, AnyTypeCreate(false))
		entryTypes = append(entryTypes, AnyTypeCreate(true))
	}

	// The original's comment: always use generic parameters for __init__.
	initType := FunctionTypeCreateSynthesizedInstance("__init__", FunctionTypeFlagsNone)
	FunctionTypeAddParam(initType, selfParam)
	FunctionTypeAddDefaultParams(initType, false)
	initType.Shared.DeclaredReturnType = evaluator.GetNoneType()
	initType.Priv.ConstructorTypeVarScopeID = GetTypeVarScopeID(classType)

	classFields.Set("__new__", SymbolCreateWithType(SymbolFlagsClassMember, constructorType, nil))
	classFields.Set("__init__", SymbolCreateWithType(SymbolFlagsClassMember, initType, nil))

	lenType := FunctionTypeCreateSynthesizedInstance("__len__", FunctionTypeFlagsNone)
	lenType.Shared.DeclaredReturnType = evaluator.GetBuiltInObject(errorNode, "int", nil)
	FunctionTypeAddParam(lenType, selfParam)
	classFields.Set("__len__", SymbolCreateWithType(SymbolFlagsClassMember, lenType, nil))

	if addGenericGetAttribute {
		getAttribType := FunctionTypeCreateSynthesizedInstance("__getattribute__", FunctionTypeFlagsNone)
		getAttribType.Shared.DeclaredReturnType = AnyTypeCreate(false)
		FunctionTypeAddParam(getAttribType, selfParam)
		FunctionTypeAddParam(getAttribType, FunctionParamCreate(
			parser.ParamCategorySimple,
			evaluator.GetBuiltInObject(errorNode, "str", nil),
			FunctionParamFlagsTypeDeclared, &nameName, nil, nil))
		classFields.Set("__getattribute__",
			SymbolCreateWithType(SymbolFlagsClassMember, getAttribType, nil))
	}

	tupleClassType := evaluator.GetBuiltInType(errorNode, "tuple")

	// The original's comment: synthesize the __match_args__ class variable.
	strType := evaluator.GetBuiltInType(errorNode, "str")
	if !addGenericGetAttribute && strType != nil && IsInstantiableClass(strType) &&
		tupleClassType != nil && IsInstantiableClass(tupleClassType) {
		literalTypes := make([]*TupleTypeArg, 0, len(matchArgsNames))
		for _, name := range matchArgsNames {
			literalTypes = append(literalTypes, &TupleTypeArg{
				Type: ClassTypeCloneAsInstance(
					ClassTypeCloneWithLiteral(strType.(*ClassType), LiteralString(name)), true),
				IsUnbounded: false,
			})
		}
		matchArgsType := ClassTypeCloneAsInstance(
			SpecializeTupleClass(tupleClassType.(*ClassType), literalTypes, true, false), true)
		classFields.Set("__match_args__",
			SymbolCreateWithType(SymbolFlagsClassMember, matchArgsType, nil))
	}

	UpdateNamedTupleBaseClass(classType, entryTypes, !addGenericGetAttribute)

	ComputeMroLinearization(classType)

	return classType
}

// namedTupleBuilder carries the state the original's two entry loops mutate.
type namedTupleBuilder struct {
	evaluator            TypeEvaluator
	fileInfo             *AnalyzerFileInfo
	classFields          SymbolTable
	constructorType      *FunctionType
	namedTupleEntries    *common.OrderedSet[string]
	allowRename          bool
	includesTypes        bool
	defaultArgCount      int
	defaultArgCountKnown bool

	matchArgsNames         []string
	entryTypes             []Type
	addGenericGetAttribute bool
}

// firstParamWithDefaultIndex is the original's
// `defaultArgCount === undefined ? 0 : Math.max(0, count - defaultArgCount)`.
// The undefined case defaults every parameter, which is the permissive answer.
func (b *namedTupleBuilder) firstParamWithDefaultIndex(entryCount int) int {
	if !b.defaultArgCountKnown {
		return 0
	}
	if n := entryCount - b.defaultArgCount; n > 0 {
		return n
	}
	return 0
}

// addEntriesFromNameString handles `namedtuple("N", "x y z")`.
func (b *namedTupleBuilder) addEntriesFromNameString(entryNameNode *parser.StringListNode) {
	entries := splitNamedTupleNames(joinStringListValue(entryNameNode))
	firstParamWithDefaultIndex := b.firstParamWithDefaultIndex(len(entries))

	for index, entryName := range entries {
		entryName = strings.TrimSpace(entryName)
		if entryName == "" {
			continue
		}

		entryName = renameUnderscore(b.evaluator, entryName, b.allowRename, entryNameNode, index)
		entryName = renameKeyword(b.evaluator, entryName, b.allowRename, entryNameNode, index)

		var entryType Type = UnknownTypeCreate(false)
		var defaultType Type
		if index >= firstParamWithDefaultIndex {
			defaultType = entryType
		}
		name := entryName
		FunctionTypeAddParam(b.constructorType, FunctionParamCreate(
			parser.ParamCategorySimple, entryType, FunctionParamFlagsTypeDeclared,
			&name, defaultType, nil))

		newSymbol := SymbolCreateWithType(SymbolFlagsInstanceMember, entryType, nil)
		b.matchArgsNames = append(b.matchArgsNames, entryName)

		// The original's comment: we need to associate the declaration with a
		// parse node. In this case it's just part of a string literal value. The
		// definition provider won't necessarily take the user to the exact spot
		// in the string, but it's close enough.
		newSymbol.AddDeclaration(&VariableDeclaration{
			DeclarationBase: DeclarationBase{
				Type:       DeclarationTypeVariable,
				Node:       entryNameNode,
				Uri:        b.fileInfo.FileUri,
				Range:      namedTupleNodeRange(entryNameNode, b.fileInfo),
				ModuleName: b.fileInfo.ModuleName,
			},
			IsRuntimeTypeExpression: true,
		})
		b.classFields.Set(entryName, newSymbol)
		b.entryTypes = append(b.entryTypes, entryType)
	}
}

// addEntriesFromSequence handles the list/tuple field-list forms, both
// `namedtuple("N", ["x", "y"])` and `NamedTuple("N", [("x", int)])`.
func (b *namedTupleBuilder) addEntriesFromSequence(entryList parser.ExpressionNode) {
	var entryExpressions []parser.ExpressionNode
	if entryList.GetNodeType() == parser.ParseNodeTypeList {
		entryExpressions = entryList.(*parser.ListNode).D.Items
	} else {
		entryExpressions = entryList.(*parser.TupleNode).D.Items
	}

	entryMap := map[string]bool{}
	firstParamWithDefaultIndex := b.firstParamWithDefaultIndex(len(entryExpressions))

	for index, entry := range entryExpressions {
		var entryTypeNode parser.ExpressionNode
		var entryType Type
		var entryNameNode parser.ExpressionNode
		entryName := ""

		if b.includesTypes {
			// The original's comment: handle the variant that includes name/type
			// tuples.
			if entry.GetNodeType() == parser.ParseNodeTypeTuple &&
				len(entry.(*parser.TupleNode).D.Items) == 2 {
				items := entry.(*parser.TupleNode).D.Items
				entryNameNode = items[0]
				entryTypeNode = items[1]
				entryType = ConvertToInstance(
					b.evaluator.GetTypeOfExpressionExpectingType(entryTypeNode, nil).Type, false)
			} else {
				b.evaluator.AddDiagnostic(DiagnosticRuleReportArgumentType,
					localization.LocMessage.NamedTupleNameType(), entry, nil)
			}
		} else {
			entryNameNode = entry
			entryType = UnknownTypeCreate(false)
		}

		if entryNameNode != nil {
			nameTypeResult := b.evaluator.GetTypeOfExpression(entryNameNode, EvalFlagsNone, nil)
			if IsClassInstance(nameTypeResult.Type) &&
				ClassTypeIsBuiltInNamed(nameTypeResult.Type.(*ClassType), "str") &&
				IsLiteralType(nameTypeResult.Type.(*ClassType)) {
				if s, ok := nameTypeResult.Type.(*ClassType).Priv.LiteralValue.(LiteralString); ok {
					entryName = string(s)
				}

				if entryName == "" {
					b.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
						localization.LocMessage.NamedTupleEmptyName(), entryNameNode, nil)
				} else {
					entryName = renameUnderscore(b.evaluator, entryName, b.allowRename, entryNameNode, index)
					entryName = renameKeyword(b.evaluator, entryName, b.allowRename, entryNameNode, index)
				}
			} else {
				b.addGenericGetAttribute = true
			}
		} else {
			b.addGenericGetAttribute = true
		}

		if entryName == "" {
			entryName = "_" + strconv.Itoa(index)
		}

		if entryMap[entryName] {
			errorNode := entryNameNode
			if errorNode == nil {
				errorNode = entry
			}
			b.evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
				localization.LocMessage.NamedTupleNameUnique(), errorNode, nil)
		}

		// The original's comment: record names in a map to detect duplicates.
		entryMap[entryName] = true

		if IsNilType(entryType) {
			entryType = UnknownTypeCreate(false)
		}

		// The untyped form's parameters are deliberately not TypeDeclared: the
		// field types are genuinely unknown, and marking them declared would make
		// strict mode report them.
		paramFlags := FunctionParamFlagsNone
		if b.includesTypes {
			paramFlags = FunctionParamFlagsTypeDeclared
		}
		var defaultType Type
		if index >= firstParamWithDefaultIndex {
			defaultType = entryType
		}
		name := entryName
		FunctionTypeAddParam(b.constructorType, FunctionParamCreate(
			parser.ParamCategorySimple, entryType, paramFlags, &name, defaultType, nil))

		b.entryTypes = append(b.entryTypes, entryType)
		b.matchArgsNames = append(b.matchArgsNames, entryName)

		newSymbol := SymbolCreateWithType(
			SymbolFlagsInstanceMember|SymbolFlagsNamedTupleMember, entryType, nil)
		if entryNameNode != nil && entryNameNode.GetNodeType() == parser.ParseNodeTypeStringList {
			decl := &VariableDeclaration{
				DeclarationBase: DeclarationBase{
					Type:       DeclarationTypeVariable,
					Node:       entryNameNode,
					Uri:        b.fileInfo.FileUri,
					Range:      namedTupleNodeRange(entryNameNode, b.fileInfo),
					ModuleName: b.fileInfo.ModuleName,
				},
			}
			if entryTypeNode != nil {
				decl.TypeAnnotationNode = entryTypeNode
			}
			newSymbol.AddDeclaration(decl)
		}
		b.classFields.Set(entryName, newSymbol)
		b.namedTupleEntries.Add(entryName)
	}

	// The original's comment: set the type in the type cache for the dict node so
	// it doesn't get evaluated again.
	b.evaluator.SetTypeResultForNode(entryList,
		&TypeResult{Type: UnknownTypeCreate(false)}, EvalFlagsNone)
}

// renameKeyword corresponds to the function of the same name.
func renameKeyword(
	evaluator TypeEvaluator, name string, allowRename bool,
	errorNode parser.ExpressionNode, index int,
) string {
	// The original's comment: determine whether the name is a keyword in python.
	if !parser.IsPythonKeyword(name, false) {
		// The original's comment: no rename necessary.
		return name
	}

	if allowRename {
		// The original's comment: rename based on index.
		return "_" + strconv.Itoa(index)
	}

	evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.NamedTupleNameKeyword(), errorNode, nil)
	return name
}

// renameUnderscore corresponds to the function of the same name.
func renameUnderscore(
	evaluator TypeEvaluator, name string, allowRename bool,
	errorNode parser.ExpressionNode, index int,
) string {
	if !strings.HasPrefix(name, "_") {
		// The original's comment: no rename necessary.
		return name
	}

	if allowRename {
		// The original's comment: rename based on index.
		return "_" + strconv.Itoa(index)
	}

	evaluator.AddDiagnostic(DiagnosticRuleReportGeneralTypeIssues,
		localization.LocMessage.NamedTupleFieldUnderscore(), errorNode, nil)

	return name
}

// findNamedTupleArg is the original's `argList.find(...)`. The two call sites
// differ in whether they also require ArgCategory.Simple.
func findNamedTupleArg(argList []*Arg, requireSimple bool, name string) *Arg {
	for _, arg := range argList {
		if requireSimple && arg.ArgCategory != parser.ArgCategorySimple {
			continue
		}
		if arg.Name != nil && arg.Name.D.Value == name {
			return arg
		}
	}
	return nil
}

// joinStringListValue is the original's
// `node.d.strings.map((s) => s.d.value).join(”)`: implicit concatenation of
// adjacent string literals.
func joinStringListValue(node *parser.StringListNode) string {
	var sb strings.Builder
	for _, s := range node.D.Strings {
		sb.WriteString(stringOrFormatValue(s))
	}
	return sb.String()
}

// splitNamedTupleNames is the original's `split(/[,\s]+/)`. JavaScript's split
// on a regex that can match at position zero yields a leading empty string,
// which the original then skips via the `if (entryName)` guard; the empty
// entries are preserved here for the same reason -- the *index* is what selects
// which fields get defaults, so dropping them would shift the boundary.
func splitNamedTupleNames(s string) []string {
	result := []string{}
	current := strings.Builder{}
	inSeparator := false

	for _, r := range s {
		isSep := r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
		if isSep {
			if !inSeparator {
				result = append(result, current.String())
				current.Reset()
				inSeparator = true
			}
			continue
		}
		inSeparator = false
		current.WriteRune(r)
	}
	result = append(result, current.String())

	return result
}

// namedTupleNodeRange is the original's convertOffsetsToRange over a node.
func namedTupleNodeRange(node parser.ParseNode, fileInfo *AnalyzerFileInfo) common.Range {
	r := node.NodeBase().TextRange
	return common.ConvertOffsetsToRange(r.Start, r.End(), fileInfo.Lines)
}
