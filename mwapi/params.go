package mwapi

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/go-querystring/query"
)

type File struct {
	Filename    string
	ContentType string
	Reader      io.Reader
}

type fileField struct {
	Field string
	File  File
}

type normalizedParams struct {
	Values url.Values
	Files  []fileField
}

func normalizeParams(p any) (normalizedParams, error) {
	var np normalizedParams
	np.Values = url.Values{}

	switch v := p.(type) {
	case nil:
		// nothing
	case url.Values:
		for k, vs := range v {
			if len(vs) == 0 {
				continue
			}
			// For MW, repeated fields are usually represented by |.
			np.Values.Set(k, strings.Join(vs, "|"))
		}
	case map[string]string:
		for k, val := range v {
			np.Values.Set(k, val)
		}
	case map[string]any:
		for k, val := range v {
			if err := addAny(&np, k, val); err != nil {
				return normalizedParams{}, err
			}
		}
	default:
		rv := reflect.ValueOf(p)
		if rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				break
			}
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			values, err := query.Values(p)
			if err != nil {
				return normalizedParams{}, err
			}
			for k, vs := range values {
				if len(vs) == 0 {
					continue
				}
				np.Values.Set(k, strings.Join(vs, "|"))
			}
		} else {
			return normalizedParams{}, fmt.Errorf("unsupported params type: %T", p)
		}
	}

	setDefaultIfMissing(np.Values, "action", "query")
	setDefaultIfMissing(np.Values, "format", "json")
	setDefaultIfMissing(np.Values, "formatversion", "2")
	setDefaultIfMissing(np.Values, "errorformat", "plaintext")

	return np, nil
}

func setDefaultIfMissing(v url.Values, key, value string) {
	if v.Get(key) == "" {
		v.Set(key, value)
	}
}

func addAny(np *normalizedParams, key string, val any) error {
	if val == nil {
		return nil
	}

	switch x := val.(type) {
	case string:
		np.Values.Set(key, x)
		return nil
	case []byte:
		np.Files = append(np.Files, fileField{
			Field: key,
			File: File{
				Filename: key,
				Reader:   bytes.NewReader(x),
			},
		})
		return nil
	case File:
		np.Files = append(np.Files, fileField{Field: key, File: x})
		return nil
	case *File:
		if x == nil {
			return nil
		}
		np.Files = append(np.Files, fileField{Field: key, File: *x})
		return nil
	case io.Reader:
		filename := key
		if f, ok := x.(*os.File); ok && f != nil {
			if name := f.Name(); name != "" {
				filename = name
			}
		}
		np.Files = append(np.Files, fileField{
			Field: key,
			File: File{
				Filename: filename,
				Reader:   x,
			},
		})
		return nil
	case bool:
		if x {
			np.Values.Set(key, "1")
		}
		return nil
	case []string:
		if len(x) == 0 {
			return nil
		}
		np.Values.Set(key, strings.Join(x, "|"))
		return nil
	case []any:
		if len(x) == 0 {
			return nil
		}
		parts := make([]string, 0, len(x))
		for _, it := range x {
			if it == nil {
				continue
			}
			parts = append(parts, fmt.Sprint(it))
		}
		if len(parts) > 0 {
			np.Values.Set(key, strings.Join(parts, "|"))
		}
		return nil
	case fmt.Stringer:
		np.Values.Set(key, x.String())
		return nil
	case int:
		np.Values.Set(key, strconv.Itoa(x))
		return nil
	case int64:
		np.Values.Set(key, strconv.FormatInt(x, 10))
		return nil
	case float64:
		np.Values.Set(key, strconv.FormatFloat(x, 'f', -1, 64))
		return nil
	default:
		rv := reflect.ValueOf(val)
		switch rv.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			np.Values.Set(key, strconv.FormatInt(rv.Int(), 10))
			return nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			np.Values.Set(key, strconv.FormatUint(rv.Uint(), 10))
			return nil
		case reflect.Float32, reflect.Float64:
			np.Values.Set(key, strconv.FormatFloat(rv.Float(), 'f', -1, 64))
			return nil
		case reflect.Slice, reflect.Array:
			parts := make([]string, 0, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				parts = append(parts, fmt.Sprint(rv.Index(i).Interface()))
			}
			if len(parts) > 0 {
				np.Values.Set(key, strings.Join(parts, "|"))
			}
			return nil
		default:
			np.Values.Set(key, fmt.Sprint(val))
			return nil
		}
	}
}
