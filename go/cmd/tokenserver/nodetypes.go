/*
 * nodetypes.go
 *
 * The "nodetypes" op: the Go half of the per-node type differential.
 *
 * Analyzes a file and reports the printed type of every name in it, keyed by
 * pre-order index, in the shape tools/ts-bridge/dump-types.ts produces from the
 * TypeScript. compare-types.js diffs the two.
 *
 * The walk is pyright's own, from NameTypeWalker in analyzer/testWalker.ts:
 * every NameNode whose parent is neither ImportFromAs nor ImportAs, and which
 * the evaluator says is reachable. See dump-types.ts for why that walk and not
 * some other one.
 *
 * With no evaluator installed every name reports "<no evaluator>". That is not
 * a placeholder for the sake of it: it keeps the two node lists the same length
 * and in the same order, so the differential's *first* scoreboard -- do both
 * sides pick out the same names -- is answerable before the evaluator exists,
 * and answers the second one honestly with zero.
 *
 * That first scoreboard is not green, and the reason is not a defect here. The
 * evaluator mutates the parse tree: typeEvaluator.ts:30085 parses a string
 * annotation on demand and grafts the result onto the StringListNode, so the
 * TypeScript side walks a tree with sub-expressions in it that this side has no
 * way to produce yet. It converges when that path is ported. See
 * tools/ts-bridge/compare-types.js.
 */

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common"
	"github.com/microsoft/pyright/go/common/uri"
	"github.com/microsoft/pyright/go/parser"
	"github.com/microsoft/pyright/go/realfs"
)

type nodeTypesRequest struct {
	FilePath string `json:"filePath"`
	// RootDirectory locates the bundled typeshed; see analyzebridge.go. When
	// empty it is derived from the file path's tests/samples ancestor, which is
	// what every corpus run wants.
	RootDirectory string `json:"rootDirectory"`
}

type nodeTypeJSON struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

// noEvaluatorMarker is what a name reports when there is no evaluator to ask.
// It is deliberately not a type name: nothing the evaluator can produce could
// collide with it and be counted as a match.
const noEvaluatorMarker = "<no evaluator>"

func handleNodeTypes(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req nodeTypesRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "nodetypes: " + err.Error()
	}

	rootDirectory := req.RootDirectory
	if rootDirectory == "" {
		root, ok := rootDirectoryForSample(req.FilePath)
		if !ok {
			return nil, "nodetypes: could not locate the reference tree above " + req.FilePath
		}
		rootDirectory = root
	}

	configOptions := analyzer.NewConfigOptions(uri.Empty())
	internalTestMode := true
	configOptions.InternalTestMode = &internalTestMode

	fileSystem := realfs.New(uri.UriExFile(rootDirectory, true, false), true)
	console := common.NewNullConsole()
	host := analyzer.NewNoAccessHost()
	importResolver := analyzer.CreateImportResolver(fileSystem, console, configOptions, host)

	program := analyzer.NewProgram(importResolver, configOptions, console, nil, nil, false, "")
	installStageDFactories(program)

	fileUri := uri.UriExFile(req.FilePath, true, false)
	program.SetTrackedFiles([]uri.Uri{fileUri})

	for program.Analyze(nil) {
	}

	nodes := []nodeTypeJSON{}
	sourceFile := program.GetSourceFile(fileUri)
	if sourceFile != nil {
		if parseResults := sourceFile.GetParseResults(); parseResults != nil {
			evaluator := program.Evaluator()

			index := 0
			var walk func(node parser.ParseNode)
			walk = func(node parser.ParseNode) {
				myIndex := index
				index++

				if nameNode, ok := node.(*parser.NameNode); ok && isEvaluatableName(nameNode) {
					nodes = append(nodes, nodeTypeJSON{
						Index: myIndex,
						Name:  nameNode.D.Value,
						Type:  typeOfName(evaluator, nameNode),
					})
				}

				for _, child := range analyzer.GetChildNodes(node) {
					if child != nil {
						walk(child)
					}
				}
			}
			walk(parseResults.ParserOutput.ParseTree)
		}
	}

	program.Dispose()

	return map[string]any{"nodes": nodes}, ""
}

// isEvaluatableName is NameTypeWalker's filter: the names in `import x as y` and
// `from m import x as y` are declarations rather than references.
func isEvaluatableName(node *parser.NameNode) bool {
	parent := node.NodeBase().Parent
	if parent == nil {
		return true
	}
	nodeType := parent.GetNodeType()
	return nodeType != parser.ParseNodeTypeImportFromAs && nodeType != parser.ParseNodeTypeImportAs
}

func typeOfName(evaluator analyzer.TypeEvaluator, node *parser.NameNode) string {
	if evaluator == nil {
		return noEvaluatorMarker
	}

	if !evaluator.IsNodeReachable(node, nil) {
		return "<unreachable>"
	}

	t := evaluator.GetType(node)
	if t == nil {
		return "<none>"
	}

	return evaluator.PrintType(t, &analyzer.PrintTypeOptions{})
}

// rootDirectoryForSample walks up from a corpus file to the directory holding
// typeshed-fallback. The TypeScript reads it from global.__rootDirectory, which
// testUtils.ts sets from the working directory; the Go binary has no such
// convention, and deriving it keeps the corpus driver from having to know it.
func rootDirectoryForSample(filePath string) (string, bool) {
	dir := common.GetDirectoryPath(filePath)
	for {
		if info, err := os.Stat(common.CombinePaths(dir, "typeshed-fallback")); err == nil && info.IsDir() {
			return dir, true
		}

		parent := common.GetDirectoryPath(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
