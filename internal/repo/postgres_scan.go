package repo

import (
	"encoding/json"
)

func marshalJSON(value any) ([]byte, error) {
	if value == nil {
		return []byte("null"), nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func unmarshalJSON(payload []byte, target any) error {
	if len(payload) == 0 || string(payload) == "null" {
		return nil
	}
	return json.Unmarshal(payload, target)
}
