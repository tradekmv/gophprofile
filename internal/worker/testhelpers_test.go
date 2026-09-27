package worker_test

import "encoding/json"

// jsonMarshal encodes v to JSON bytes for use in tests.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
