package store

import (
	"bytes"
	"encoding/gob"
)

func encode(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(data []byte, v interface{}) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}
