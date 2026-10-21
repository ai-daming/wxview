package webapi

import (
	"net/http/httptest"
	"net/url"
	"testing"
	wxquery "wxview/internal/query"
)

func TestNextMessageCursorURLFreezesQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/messages?username=synthetic&date=today&order=desc&limit=100&no_index=true&source=true&refresh=true", nil)
	result := wxquery.MessagesResult{Order: "desc", NextCursor: "opaque", Range: wxquery.TimeRange{Start: 10, End: 20, HasStart: true, HasEnd: true}}
	next, err := url.Parse(nextMessagesCursorURL(req, result))
	if err != nil {
		t.Fatal(err)
	}
	q := next.Query()
	for key, want := range map[string]string{"username": "synthetic", "start": "10", "end": "20", "order": "desc", "cursor": "opaque", "limit": "100", "no_index": "true", "source": "true"} {
		if q.Get(key) != want {
			t.Fatalf("%s=%s want %s", key, q.Get(key), want)
		}
	}
	for _, key := range []string{"date", "offset", "after_seq", "refresh"} {
		if q.Has(key) {
			t.Fatalf("retained %s", key)
		}
	}
}
