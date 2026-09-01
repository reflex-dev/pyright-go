package parser

import "testing"

func TestParseNodeTypeMapIsComplete(t *testing.T) {
	// The map must cover every node type, since the reverse map is derived from
	// it and is used to render node types by name.
	if len(ParseNodeTypeMap) != 78 {
		t.Errorf("ParseNodeTypeMap has %d entries, want 78", len(ParseNodeTypeMap))
	}
	if len(ParseNodeTypeNameMap) != len(ParseNodeTypeMap) {
		t.Errorf("the forward and reverse maps disagree: %d vs %d",
			len(ParseNodeTypeMap), len(ParseNodeTypeNameMap))
	}

	// Every node type value from 0 to the last must be present exactly once.
	for nodeType := ParseNodeTypeError; nodeType <= ParseNodeTypeTypeAlias; nodeType++ {
		name, ok := ParseNodeTypeNameMap[nodeType]
		if !ok {
			t.Errorf("no name for node type %d", int(nodeType))
			continue
		}
		if ParseNodeTypeMap[name] != nodeType {
			t.Errorf("round trip failed for %q: %d != %d", name, ParseNodeTypeMap[name], nodeType)
		}
	}
}

func TestParseNodeTypeMapSpotChecks(t *testing.T) {
	for name, want := range map[string]ParseNodeType{
		"Error":      ParseNodeTypeError,
		"Module":     ParseNodeTypeModule,
		"TypeAlias":  ParseNodeTypeTypeAlias,
		"StringList": ParseNodeTypeStringList,
	} {
		if got := ParseNodeTypeMap[name]; got != want {
			t.Errorf("ParseNodeTypeMap[%q] = %d, want %d", name, got, want)
		}
	}
}

func TestOperatorTypeMap(t *testing.T) {
	for text, want := range map[string]OperatorType{
		"+":      OperatorTypeAdd,
		"//=":    OperatorTypeFloorDivideEqual,
		"<>":     OperatorTypeLessOrGreaterThan,
		"**":     OperatorTypePower,
		"and":    OperatorTypeAnd,
		"is not": OperatorTypeIsNot,
		"not in": OperatorTypeNotIn,
	} {
		if got := OperatorTypeMap[text]; got != want {
			t.Errorf("OperatorTypeMap[%q] = %d, want %d", text, got, want)
		}
	}

	// The "not" entry carries a trailing space in the original; preserve it
	// rather than silently normalizing the key.
	if _, ok := OperatorTypeMap["not "]; !ok {
		t.Error(`expected the key "not " (with a trailing space), as in the original`)
	}
	if _, ok := OperatorTypeMap["not"]; ok {
		t.Error(`the original has no bare "not" key`)
	}
}

func TestOperatorTypeNameMapRoundTrips(t *testing.T) {
	if len(OperatorTypeNameMap) != len(OperatorTypeMap) {
		t.Errorf("the forward and reverse operator maps disagree: %d vs %d",
			len(OperatorTypeMap), len(OperatorTypeNameMap))
	}
	for text, value := range OperatorTypeMap {
		if OperatorTypeNameMap[value] != text {
			t.Errorf("round trip failed for %q: got %q", text, OperatorTypeNameMap[value])
		}
	}
}
