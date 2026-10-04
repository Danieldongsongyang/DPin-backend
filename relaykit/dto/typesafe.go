package dto

import (
	"encoding/json"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// TypeSafeRequest keeps the provider's structured fields intact. RawBody is
// forwarded by the host after model mapping, so unknown future fields survive.
type TypeSafeRequest struct {
	Model     string                     `json:"model"`
	State     json.RawMessage            `json:"state"`
	Questions map[string]json.RawMessage `json:"questions"`
	Stream    *bool                      `json:"stream,omitempty"`
	RawBody   []byte                     `json:"-"`
}

func (r *TypeSafeRequest) GetTokenCountMeta() *types.TokenCountMeta {
	return &types.TokenCountMeta{
		TokenType:   types.TokenTypeTokenizer,
		CombineText: string(r.RawBody),
	}
}

func (r *TypeSafeRequest) IsStream(*http.Request) bool { return false }

func (r *TypeSafeRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
