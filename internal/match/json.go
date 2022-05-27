package match

import (
	"fmt"
	"reflect"
	"strings"

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
		if !res.Exists() {
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

// JSONKeyMissing returns a matcher which will check that `forbiddenKey` is not present in the JSON object.
// `forbiddenKey` can be nested, see https://godoc.org/github.com/tidwall/gjson#Get for details.
func JSONKeyMissing(forbiddenKey string) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, forbiddenKey)
		if res.Exists() {
			return fmt.Errorf("key '%s' present", forbiddenKey)
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

// JSONKeyArrayOfSize returns a matcher which will check that `wantKey` is present and
// its value is an array with the given size.
// `wantKey` can be nested, see https://godoc.org/github.com/tidwall/gjson#Get for details.
func JSONKeyArrayOfSize(wantKey string, wantSize int) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if !res.Exists() {
			return fmt.Errorf("key '%s' missing", wantKey)
		}
		if !res.IsArray() {
			return fmt.Errorf("key '%s' is not an array", wantKey)
		}
		entries := res.Array()
		if len(entries) != wantSize {
			return fmt.Errorf("key '%s' is an array of the wrong size, got %v want %v", wantKey, len(entries), wantSize)
		}
		return nil
	}
}

func jsonCheckOffInternal(wantKey string, wantItems []interface{}, allowUnwantedItems bool, mapper func(gjson.Result) interface{}, fn func(interface{}, gjson.Result) error) JSON {
	return func(body []byte) error {
		res := gjson.GetBytes(body, wantKey)
		if !res.Exists() {
			return fmt.Errorf("missing key '%s'", wantKey)
		}
		if !res.IsArray() && !res.IsObject() {
			return fmt.Errorf("JSONCheckOff: key '%s' is not an array or object", wantKey)
		}
		var err error
		res.ForEach(func(key, val gjson.Result) bool {
			itemRes := key
			if res.IsArray() {
				itemRes = val
			}
			// convert it to something we can check off
			item := mapper(itemRes)
			if item == nil {
				err = fmt.Errorf("JSONCheckOff: mapper function mapped %v to nil", itemRes.Raw)
				return false
			}

			// check off the item
			want := -1
			for i, w := range wantItems {
				if reflect.DeepEqual(w, item) {
					want = i
					break
				}
			}
			if !allowUnwantedItems && want == -1 {
				err = fmt.Errorf("JSONCheckOff: unexpected item %s", item)
				return false
			}

			if want != -1 {
				// delete the wanted item
				wantItems = append(wantItems[:want], wantItems[want+1:]...)
			}

			// do further checks
			if fn != nil {
				err = fn(item, val)
				if err != nil {
					return false
				}
			}
			return true
		})

		// at this point we should have gone through all of wantItems.
		// If we haven't then we expected to see some items but didn't.
		if err == nil && len(wantItems) > 0 {
			err = fmt.Errorf("JSONCheckOff: did not see items: %v", wantItems)
		}

		return err
	}
}

// JSONCheckOffAllowUnwanted returns a matcher which will loop over `wantKey` and ensure that the items
// (which can be array elements or object keys)
// are present exactly once in any order in `wantItems`. Allows unexpected items or items
// appear that more than once. This matcher can be used to check off items in
// an array/object. The `mapper` function should map the item to an interface which will be
// comparable via `reflect.DeepEqual` with items in `wantItems`. The optional `fn` callback
// allows more checks to be performed other than checking off the item from the list. It is
// called with 2 args: the result of the `mapper` function and the element itself (or value if
// it's an object).
//
// Usage: (ensures `events` has these events in any order, with the right event type)
//    JSONCheckOffAllowUnwanted("events", []interface{}{"$foo:bar", "$baz:quuz"}, func(r gjson.Result) interface{} {
//        return r.Get("event_id").Str
//    }, func(eventID interface{}, eventBody gjson.Result) error {
//        if eventBody.Get("type").Str != "m.room.message" {
//	          return fmt.Errorf("expected event to be 'm.room.message'")
//        }
//    })
func JSONCheckOffAllowUnwanted(wantKey string, wantItems []interface{}, mapper func(gjson.Result) interface{}, fn func(interface{}, gjson.Result) error) JSON {
	return jsonCheckOffInternal(wantKey, wantItems, true, mapper, fn)
}

// JSONCheckOff returns a matcher which will loop over `wantKey` and ensure that the items
// (which can be array elements or object keys)
// are present exactly once in any order in `wantItems`. If there are unexpected items or items
// appear more than once then the match fails. This matcher can be used to check off items in
// an array/object. The `mapper` function should map the item to an interface which will be
// comparable via `reflect.DeepEqual` with items in `wantItems`. The optional `fn` callback
// allows more checks to be performed other than checking off the item from the list. It is
// called with 2 args: the result of the `mapper` function and the element itself (or value if
// it's an object).
//
// Usage: (ensures `events` has these events in any order, with the right event type)
//    JSONCheckOff("events", []interface{}{"$foo:bar", "$baz:quuz"}, func(r gjson.Result) interface{} {
//        return r.Get("event_id").Str
//    }, func(eventID interface{}, eventBody gjson.Result) error {
//        if eventBody.Get("type").Str != "m.room.message" {
//	          return fmt.Errorf("expected event to be 'm.room.message'")
//        }
//    })
func JSONCheckOff(wantKey string, wantItems []interface{}, mapper func(gjson.Result) interface{}, fn func(interface{}, gjson.Result) error) JSON {
	return jsonCheckOffInternal(wantKey, wantItems, false, mapper, fn)
}

// JSONArrayEach returns a matcher which will check that `wantKey` is an array then loops over each
// item calling `fn`. If `fn` returns an error, iterating stops and an error is returned.
func JSONArrayEach(wantKey string, fn func(gjson.Result) error) JSON {
	return func(body []byte) error {
		var res gjson.Result
		if wantKey == "" {
			res = gjson.ParseBytes(body)
		} else {
			res = gjson.GetBytes(body, wantKey)
		}

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

// JSONArraySome returns a matcher which will check that `wantKey` is an array then loops over each
// item calling `fn`. If `fn` returns an error for all items, an error is returned.
func JSONArraySome(wantKey string, fn func(gjson.Result) error) JSON {
	return func(body []byte) error {
		var res gjson.Result
		if wantKey == "" {
			res = gjson.ParseBytes(body)
		} else {
			res = gjson.GetBytes(body, wantKey)
		}

		if !res.Exists() {
			return fmt.Errorf("missing key '%s'", wantKey)
		}
		if !res.IsArray() {
			return fmt.Errorf("key '%s' is not an array", wantKey)
		}
		var err error
		res.ForEach(func(_, val gjson.Result) bool {
			err = fn(val)
			return err != nil
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

// AnyOf takes 1 or more `checkers`, and builds a new checker which accepts a given
// json body iff it's accepted by at least one of the original `checkers`.
func AnyOf(checkers ...JSON) JSON {
	return func(body []byte) error {
		if len(checkers) == 0 {
			return fmt.Errorf("must provide at least one checker to AnyOf")
		}

		errors := make([]error, len(checkers))
		for i, check := range checkers {
			errors[i] = check(body)
			if errors[i] == nil {
				return nil
			}
		}

		builder := strings.Builder{}
		builder.WriteString("all checks failed:")
		for _, err := range errors {
			builder.WriteString("\n    ")
			builder.WriteString(err.Error())
		}
		return fmt.Errorf(builder.String())
	}
}
