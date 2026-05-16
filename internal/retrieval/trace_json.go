package retrieval

import (
	"encoding/json"

	"github.com/DotBlood/ioc/internal/model"
)

// TraceAsJSON formats a RetrievalTrace as indented JSON for debugging.
func TraceAsJSON(t *model.RetrievalTrace) (string, error) {
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
