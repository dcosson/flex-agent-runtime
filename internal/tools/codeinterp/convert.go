package codeinterp

import (
	"fmt"
	"sort"

	"go.starlark.net/starlark"
)

func toStarlark(v any) (starlark.Value, error) {
	switch x := v.(type) {
	case nil:
		return starlark.None, nil
	case string:
		return starlark.String(x), nil
	case bool:
		return starlark.Bool(x), nil
	case int:
		return starlark.MakeInt(x), nil
	case int64:
		return starlark.MakeInt64(x), nil
	case float64:
		return starlark.Float(x), nil
	case []any:
		vals := make([]starlark.Value, 0, len(x))
		for _, it := range x {
			sv, err := toStarlark(it)
			if err != nil {
				return nil, err
			}
			vals = append(vals, sv)
		}
		return starlark.NewList(vals), nil
	case map[string]any:
		d := starlark.NewDict(len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sv, err := toStarlark(x[k])
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, err
			}
		}
		return d, nil
	default:
		return nil, fmt.Errorf("%w: unsupported go type %T", ErrConversion, v)
	}
}

func fromStarlark(v starlark.Value) (any, error) {
	switch x := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.String:
		return x.GoString(), nil
	case starlark.Bool:
		return bool(x), nil
	case starlark.Int:
		i, ok := x.Int64()
		if ok {
			return i, nil
		}
		f := x.Float()
		return int64(f), nil
	case starlark.Float:
		return float64(x), nil
	case *starlark.List:
		out := make([]any, 0, x.Len())
		for i := 0; i < x.Len(); i++ {
			gv, err := fromStarlark(x.Index(i))
			if err != nil {
				return nil, err
			}
			out = append(out, gv)
		}
		return out, nil
	case *starlark.Dict:
		out := map[string]any{}
		for _, item := range x.Items() {
			k, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("%w: map key must be string", ErrConversion)
			}
			gv, err := fromStarlark(item[1])
			if err != nil {
				return nil, err
			}
			out[k.GoString()] = gv
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: unsupported starlark type %s", ErrConversion, v.Type())
	}
}
