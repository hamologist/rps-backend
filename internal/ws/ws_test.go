package ws

import (
	"slices"
	"testing"
)

func TestAcceptOptions(t *testing.T) {
	o := AcceptOptions([]string{"http://localhost:5173", "https://rps.example.com"})
	if o.InsecureSkipVerify || !slices.Equal(o.OriginPatterns, []string{"localhost:5173", "rps.example.com"}) {
		t.Fatalf("%+v", o)
	}
	if o := AcceptOptions([]string{"http://a.com", "*"}); !o.InsecureSkipVerify || o.OriginPatterns != nil {
		t.Fatalf("wildcard: %+v", o)
	}
	if o := AcceptOptions(nil); o.InsecureSkipVerify || o.OriginPatterns != nil {
		t.Fatalf("empty: %+v", o)
	}
}
