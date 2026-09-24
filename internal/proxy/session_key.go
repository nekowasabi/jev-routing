package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// sessionKeyFromBody groups requests with the same opaque Claude user_id.
// The proxy instance ID keeps exported keys unlinkable across runs; the raw ID
// is never retained in an event or benchmark record.
func sessionKeyFromBody(body []byte, instanceID string) string {
	var request struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &request) != nil || request.Metadata.UserID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(instanceID + "\x00" + request.Metadata.UserID))
	return hex.EncodeToString(sum[:12])
}
