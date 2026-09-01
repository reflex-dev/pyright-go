/*
 * typebridge.go
 *
 * The "types" op, which lets pyright's own typePrinter.test.ts run unmodified
 * against the Go type model and type printer.
 *
 * The Node shim does not build TypeScript type objects and ship them over the
 * wire. It records the construction calls the test makes -- ClassType.specialize,
 * FunctionType.addParam, `x.shared.declaredReturnType = y` and so on -- into a
 * command log, and sends the whole log with each printType call. This side
 * replays the log against the Go type model and then prints. Replay from
 * scratch is what makes the one-process-per-request client in
 * tools/ts-bridge/client.ts workable: there is no session state to keep.
 *
 * The consequence, and it is the point, is that this exercises the Go
 * types.ts port as well as the Go typePrinter.ts port. Nothing but the
 * assertions and the shape of the test remains TypeScript.
 *
 * One deviation is documented in shim-typePrinter.ts: the test's
 * returnTypeCallback is a TypeScript closure, and the protocol is
 * unidirectional, so it is reimplemented here. It is two lines and it is
 * identical to the test's.
 */

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/microsoft/pyright/go/analyzer"
	"github.com/microsoft/pyright/go/common/uri"
)

// typeCmd is one recorded operation. Only the fields relevant to Cmd are set.
type typeCmd struct {
	Cmd string `json:"cmd"`

	// call
	ID     int               `json:"id"`
	Target string            `json:"target"`
	Args   []json.RawMessage `json:"args"`

	// get/set/push
	Handle int               `json:"handle"`
	Path   []string          `json:"path"`
	Value  json.RawMessage   `json:"value"`
	Values []json.RawMessage `json:"values"`
}

// typesRequest is the payload of the "types" op: the log to replay, then the
// type to print.
type typesRequest struct {
	Log    []typeCmd `json:"log"`
	Handle int       `json:"handle"`
	Flags  int       `json:"flags"`
}

type typeBridge struct {
	handles map[int]any
}

// returnTypeCallback reimplements the callback defined at the top of
// typePrinter.test.ts.
func returnTypeCallback(t *analyzer.FunctionType) analyzer.Type {
	if t.Shared.DeclaredReturnType != nil {
		return t.Shared.DeclaredReturnType
	}
	return analyzer.UnknownTypeCreate(true)
}

func handleTypes(payload json.RawMessage) (result any, errMsg string) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			errMsg = fmt.Sprint(r)
		}
	}()

	var req typesRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, "types: " + err.Error()
	}

	b := &typeBridge{handles: map[int]any{}}
	for _, cmd := range req.Log {
		if err := b.replay(cmd); err != nil {
			return nil, err.Error()
		}
	}

	target, ok := b.handles[req.Handle]
	if !ok {
		return nil, fmt.Sprintf("types: unknown handle %d", req.Handle)
	}
	t, ok := target.(analyzer.Type)
	if !ok {
		return nil, fmt.Sprintf("types: handle %d is not a type", req.Handle)
	}

	return analyzer.PrintType(t, analyzer.PrintTypeFlags(req.Flags), returnTypeCallback), ""
}

func (b *typeBridge) replay(cmd typeCmd) error {
	switch cmd.Cmd {
	case "call":
		args := make([]any, 0, len(cmd.Args))
		for _, raw := range cmd.Args {
			args = append(args, b.decode(raw))
		}
		value, err := b.dispatch(cmd.Target, args)
		if err != nil {
			return err
		}
		b.handles[cmd.ID] = value
		return nil

	case "set":
		field, err := b.resolvePath(cmd.Handle, cmd.Path)
		if err != nil {
			return err
		}
		return assign(field, b.decode(cmd.Value))

	case "push":
		field, err := b.resolvePath(cmd.Handle, cmd.Path)
		if err != nil {
			return err
		}
		for _, raw := range cmd.Values {
			converted, err := convert(b.decode(raw), field.Type().Elem())
			if err != nil {
				return err
			}
			field.Set(reflect.Append(field, converted))
		}
		return nil
	}

	return fmt.Errorf("types: unknown command %q", cmd.Cmd)
}

// resolvePath walks a dotted property path from a handle down to a settable
// field, matching JavaScript property names against Go field names
// case-insensitively. Pointers are followed, so `shared.typeParams` reaches
// (*ClassDetailsShared).TypeParams.
func (b *typeBridge) resolvePath(handle int, path []string) (reflect.Value, error) {
	root, ok := b.handles[handle]
	if !ok {
		return reflect.Value{}, fmt.Errorf("types: unknown handle %d", handle)
	}

	v := reflect.ValueOf(root)
	for _, name := range path {
		for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
			if v.IsNil() {
				return reflect.Value{}, fmt.Errorf("types: nil while resolving %q", strings.Join(path, "."))
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, fmt.Errorf("types: %q is not a struct field path", strings.Join(path, "."))
		}
		field := v.FieldByNameFunc(func(candidate string) bool {
			return strings.EqualFold(candidate, name)
		})
		if !field.IsValid() {
			return reflect.Value{}, fmt.Errorf("types: no field %q in %s", name, v.Type())
		}
		v = field
	}

	if !v.CanSet() {
		return reflect.Value{}, fmt.Errorf("types: %q is not settable", strings.Join(path, "."))
	}
	return v, nil
}

func assign(field reflect.Value, value any) error {
	converted, err := convert(value, field.Type())
	if err != nil {
		return err
	}
	field.Set(converted)
	return nil
}

// convert coerces a decoded wire value to the Go type of the field it is going
// into. JSON numbers arrive as float64 and have to be narrowed.
func convert(value any, target reflect.Type) (reflect.Value, error) {
	if value == nil {
		return reflect.Zero(target), nil
	}

	v := reflect.ValueOf(value)
	if v.Type().AssignableTo(target) {
		return v, nil
	}
	if v.Type().ConvertibleTo(target) && target.Kind() != reflect.String {
		return v.Convert(target), nil
	}

	return reflect.Value{}, fmt.Errorf("types: cannot assign %T to %s", value, target)
}

// decode turns one wire value into a Go value. Objects tagged $h are handles
// recorded earlier in the log; objects tagged $uri are pyright Uris, which this
// stage of the port represents with the constant/empty forms only.
func (b *typeBridge) decode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}

	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		panic("types: " + err.Error())
	}

	switch value := probe.(type) {
	case nil, bool, float64, string:
		return value

	case []any:
		out := make([]any, 0, len(value))
		for _, element := range value {
			encoded, err := json.Marshal(element)
			if err != nil {
				panic("types: " + err.Error())
			}
			out = append(out, b.decode(encoded))
		}
		return out

	case map[string]any:
		if id, ok := value["$h"]; ok {
			handle, ok := b.handles[int(id.(float64))]
			if !ok {
				panic(fmt.Sprintf("types: unknown handle %v", id))
			}
			return handle
		}
		if key, ok := value["$uri"]; ok {
			if name, _ := key.(string); name != "" {
				return uri.Constant(name)
			}
			return uri.Empty()
		}
	}

	panic(fmt.Sprintf("types: cannot decode %s", string(raw)))
}
