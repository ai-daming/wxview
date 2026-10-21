package query

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"wxview/internal/messages"
)

type messagePageIdentity struct {
	Account  string `json:"account"`
	DataDir  string `json:"data_dir"`
	Username string `json:"username"`
	Order    string `json:"order"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	HasStart bool   `json:"has_start"`
	HasEnd   bool   `json:"has_end"`
}

type messageCursor struct {
	Version  int              `json:"v"`
	Identity [32]byte         `json:"query"`
	After    messages.PageKey `json:"after"`
}

func messageIdentityHash(identity messagePageIdentity) [32]byte {
	data, _ := json.Marshal(identity)
	return sha256.Sum256(data)
}

// The checksum detects corruption, not hostile forgery. Cursors confer no access.
func encodeMessageCursor(identity messagePageIdentity, after messages.PageKey) string {
	data, _ := json.Marshal(messageCursor{1, messageIdentityHash(identity), after})
	sum := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(append(data, sum[:]...))
}

func decodeMessageCursor(token string, identity messagePageIdentity) (*messages.PageKey, error) {
	invalid := fmt.Errorf("invalid message cursor or query mismatch: restart from the first page with the same account, username, order and time range")
	if len(token) > 4096 {
		return nil, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return nil, invalid
	}
	data, checksum := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	sum := sha256.Sum256(data)
	if !bytes.Equal(sum[:], checksum) {
		return nil, invalid
	}
	var cursor messageCursor
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&cursor) != nil || dec.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Identity != messageIdentityHash(identity) || cursor.After.SourceDB == "" {
		return nil, invalid
	}
	return &cursor.After, nil
}
