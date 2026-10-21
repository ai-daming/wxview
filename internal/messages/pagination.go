package messages

import (
	"fmt"
	"strings"
)

// PageKey is the total order shared by shard scans and the derived index.
// SourceDB disambiguates local IDs which are only unique within a shard.
type PageKey struct {
	CreateTime int64  `json:"time"`
	Seq        int64  `json:"seq"`
	LocalID    int64  `json:"local_id"`
	SourceDB   string `json:"source_db"`
}

func NormalizeOrder(order string) (string, error) {
	order = strings.ToLower(strings.TrimSpace(order))
	if order == "" {
		order = "asc"
	}
	if order != "asc" && order != "desc" {
		return "", fmt.Errorf("invalid order: use asc or desc")
	}
	return order, nil
}

func KeyForPage(m Message) PageKey { return PageKey{m.CreateTime, m.Seq, m.LocalID, m.SourceDB} }

func PageKeyLess(a, b PageKey, order string) bool {
	if order == "desc" {
		a, b = b, a
	}
	if a.CreateTime != b.CreateTime {
		return a.CreateTime < b.CreateTime
	}
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	if a.LocalID != b.LocalID {
		return a.LocalID < b.LocalID
	}
	return a.SourceDB < b.SourceDB
}
