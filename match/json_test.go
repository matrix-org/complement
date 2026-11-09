package match

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

func TestJSONArraySome(t *testing.T) {
	t.Run("parallel", func(t *testing.T) {
		for _, testCase := range []struct {
			name       string
			jsonString string
			wantErr    bool
		}{
			{
				name:       "passes when target is first",
				jsonString: `{ "test": [1,3,5] }`,
				wantErr:    false,
			},
			{
				name:       "passes when target is last",
				jsonString: `{ "test": [1,2,3] }`,
				wantErr:    false,
			},
			{
				name:       "passes when target is in the middle",
				jsonString: `{ "test": [1,3,5] }`,
				wantErr:    false,
			},
			{
				name:       "fails when target not in array",
				jsonString: `{ "test": [1,5,10] }`,
				wantErr:    true,
			},
			{
				name:       "fails when not array",
				jsonString: `{ "test": 3 }`,
				wantErr:    true,
			},
			{
				name:       "fails when missing key",
				jsonString: `{ }`,
				wantErr:    true,
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				t.Parallel()

				matcher := JSONArraySome("test", func(field gjson.Result) error {
					value := field.Int()
					if value == 3 {
						// Found target
						return nil
					}
					return fmt.Errorf("Expected to find target 3, found '%d'", value)
				})

				err := matcher(gjson.Parse(testCase.jsonString))
				if (err != nil) != testCase.wantErr {
					t.Errorf("JSONArraySome() error = %v, wantErr %v", err, testCase.wantErr)
				}
			})
		}
	})
}
