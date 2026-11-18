package utils

import "testing"

func TestNormalizePaging(t *testing.T) {
	cases := []struct {
		name       string
		limit      int
		offset     int
		wantLimit  int
		wantOffset int
	}{
		{name: "defaults", limit: 0, offset: 0, wantLimit: 20, wantOffset: 0},
		{name: "negative limit", limit: -5, offset: 0, wantLimit: 20, wantOffset: 0},
		{name: "limit within range", limit: 50, offset: 10, wantLimit: 50, wantOffset: 10},
		{name: "limit capped", limit: 500, offset: 0, wantLimit: 200, wantOffset: 0},
		{name: "negative offset", limit: 10, offset: -1, wantLimit: 10, wantOffset: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset := NormalizePaging(tc.limit, tc.offset)
			if limit != tc.wantLimit || offset != tc.wantOffset {
				t.Fatalf("got (%d, %d), want (%d, %d)", limit, offset, tc.wantLimit, tc.wantOffset)
			}
		})
	}
}
