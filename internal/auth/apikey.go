package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	ErrMissingAPIKey = errors.New("missing API key")
	ErrInvalidAPIKey = errors.New("invalid API key")
)

type Principal struct {
	ID   string
	Plan string
}

type APIKeyAuthenticator struct {
	records []apiKeyRecord
}

type apiKeyRecord struct {
	keyHash   string
	principal Principal
}

// NewAPIKeyAuthenticatorFromHashes parses:
//
//	API_KEY_HASHES="sha256hash:user-1:free,sha256hash:user-2:pro"
//
// The raw API keys are never stored in the gateway config.
func NewAPIKeyAuthenticatorFromHashes(value string) (*APIKeyAuthenticator, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("API_KEY_HASHES must not be empty")
	}
	rawRecords := strings.Split(value, ",")

	records := make([]apiKeyRecord, 0, len(rawRecords))

	for _, rawRecord := range rawRecords {
		parts := strings.Split(rawRecord, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid API key record %q; expected sha256_hash:principal_id:plan", rawRecord)
		}

		keyHash := strings.ToLower(strings.TrimSpace(parts[0]))
		principalID := strings.TrimSpace(parts[1])
		plan := strings.TrimSpace(parts[2])

		if keyHash == "" || principalID == "" || plan == "" {
			return nil, fmt.Errorf("invalid API key record %q; hash, principal_id, and plan are required", rawRecord)
		}

		if len(keyHash) != 64 {
			return nil, fmt.Errorf("invalid API key hash %q; expected 64-character SHA-256 hex string", keyHash)
		}

		if _, err := hex.DecodeString(keyHash); err != nil {
			return nil, fmt.Errorf("invalid API key hash %q: %w", keyHash, err)
		}

		records = append(records, apiKeyRecord{
			keyHash: keyHash,
			principal: Principal{
				ID:   principalID,
				Plan: plan,
			},
		})
	}

	return &APIKeyAuthenticator{
		records: records,
	}, nil
}

func (a *APIKeyAuthenticator) Authenticate(r *http.Request) (Principal, error) {
	apiKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if apiKey == "" {
		return Principal{}, ErrMissingAPIKey
	}

	incomingHash := HashAPIKey(apiKey)

	for _, record := range a.records {
		if constantTimeEqual(incomingHash, record.keyHash) {
			return record.principal, nil
		}
	}

	return Principal{}, ErrInvalidAPIKey
}

func HashAPIKey(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
