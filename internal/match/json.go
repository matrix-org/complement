package match

import (
	"fmt"
	"reflect"

	"github.com/tidwall/gjson"
)

// JSON will perform some matches on the given JSON body, returning an error on a mis-match.
// It can be assumed that the bytes are valid JSON.
type JSON func(body []byte) error

// JSONKeyEqual returns a matcher which will check that `wantKey` is present and its value matches `wantValue`.
// `wantKey` can be nested, see https://godoc.org/github.com/tidwall/gjson#Get for details.
// `wantValue` is matched via reflect.DeepEqual and the JSON takes the forms according to https://godoc.org/github.com/tidwall/gjson#Result.Value
func JSONKeyEqual(wantKey string, wantValue interface{}) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if res.Index == 0 {
			return fmt.Errorf("key '%s' missing", wantKey)
		}
		gotValue := res.Value()
		if !reflect.DeepEqual(gotValue, wantValue) {
			return fmt.Errorf("key '%s' got '%v' want '%v'", wantKey, gotValue, wantValue)
		}
		return nil
	}
}

// JSONKeyPresent returns a matcher which will check that `wantKey` is present in the JSON object.
// `wantKey` can be nested, see https://godoc.org/github.com/tidwall/gjson#Get for details.
func JSONKeyPresent(wantKey string) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if !res.Exists() {
			return fmt.Errorf("key '%s' missing", wantKey)
		}
		return nil
	}
}

// JSONKeyTypeEqual returns a matcher which will check that `wantKey` is present and its value is of the type `wantType`.
// `wantKey` can be nested, see https://godoc.org/github.com/tidwall/gjson#Get for details.
func JSONKeyTypeEqual(wantKey string, wantType gjson.Type) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if !res.Exists() {
			return fmt.Errorf("key '%s' missing", wantKey)
		}
		if res.Type != wantType {
			return fmt.Errorf("key '%s' is of the wrong type, got %s want %s", wantKey, res.Type, wantType)
		}
		return nil
	}
}

// JSONArrayEach returns a matcher which will check that `wantKey` is an array then loops over each
// item calling `fn`. If `fn` returns an error, iterating stops and an error is returned.
func JSONArrayEach(wantKey string, fn func(gjson.Result) error) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if !res.Exists() {
			return fmt.Errorf("missing key '%s'", wantKey)
		}
		if !res.IsArray() {
			return fmt.Errorf("key '%s' is not an array", wantKey)
		}
		var err error
		res.ForEach(func(_, val gjson.Result) bool {
			err = fn(val)
			return err == nil
		})
		return err
	}
}

// JSONMapEach returns a matcher which will check that `wantKey` is a map then loops over each
// item calling `fn`. If `fn` returns an error, iterating stops and an error is returned.
func JSONMapEach(wantKey string, fn func(k, v gjson.Result) error) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if !res.Exists() {
			return fmt.Errorf("missing key '%s'", wantKey)
		}
		if !res.IsObject() {
			return fmt.Errorf("key '%s' is not an object", wantKey)
		}
		var err error
		res.ForEach(func(key, val gjson.Result) bool {
			err = fn(key, val)
			return err == nil
		})
		return err
	}
}
