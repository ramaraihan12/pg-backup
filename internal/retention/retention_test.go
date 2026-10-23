package retention

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func items(times ...string) []Item {
	var out []Item
	for _, s := range times {
		out = append(out, Item{ID: s, Time: at(s)})
	}
	return out
}

func kept(ds []Decision) []string {
	var out []string
	for _, d := range ds {
		if d.Keep {
			out = append(out, d.ID)
		}
	}
	return out
}

func TestPlan(t *testing.T) {
	tests := []struct {
		name   string
		items  []Item
		policy Policy
		want   []string // kept ids, newest first
	}{
		{
			name:   "empty input",
			policy: Policy{Daily: 7},
			want:   nil,
		},
		{
			name:   "single backup is kept",
			items:  items("2026-10-01 03:00"),
			policy: Policy{Daily: 1},
			want:   []string{"2026-10-01 03:00"},
		},
		{
			name:   "daily keeps newest of each day",
			items:  items("2026-10-03 03:00", "2026-10-03 15:00", "2026-10-02 03:00", "2026-10-01 03:00"),
			policy: Policy{Daily: 2},
			want:   []string{"2026-10-03 15:00", "2026-10-02 03:00"},
		},
		{
			name:   "daily counts only days that have a backup",
			items:  items("2026-10-20 03:00", "2026-10-10 03:00", "2026-10-01 03:00", "2026-09-01 03:00"),
			policy: Policy{Daily: 3},
			want:   []string{"2026-10-20 03:00", "2026-10-10 03:00", "2026-10-01 03:00"},
		},
		{
			name: "weekly keeps newest of each iso week",
			// 2026-10-05 is a Monday. Weeks: Oct 5-11, Sep 28-Oct 4, Sep 21-27.
			items: items(
				"2026-10-09 03:00", "2026-10-06 03:00",
				"2026-10-02 03:00", "2026-09-29 03:00",
				"2026-09-25 03:00", "2026-09-22 03:00",
			),
			policy: Policy{Weekly: 2},
			want:   []string{"2026-10-09 03:00", "2026-10-02 03:00"},
		},
		{
			name: "iso week crosses the year boundary",
			// 2025-12-29 (Mon) to 2026-01-04 (Sun) is one ISO week, 2026-W01.
			items:  items("2026-01-03 03:00", "2025-12-30 03:00", "2025-12-26 03:00"),
			policy: Policy{Weekly: 2},
			want:   []string{"2026-01-03 03:00", "2025-12-26 03:00"},
		},
		{
			name:   "monthly keeps newest of each month",
			items:  items("2026-10-20 03:00", "2026-10-01 03:00", "2026-09-30 03:00", "2026-09-02 03:00", "2026-08-15 03:00"),
			policy: Policy{Monthly: 2},
			want:   []string{"2026-10-20 03:00", "2026-09-30 03:00"},
		},
		{
			name: "rules overlap and union",
			items: items(
				"2026-10-10 03:00", "2026-10-09 03:00", "2026-10-08 03:00",
				"2026-09-30 03:00", "2026-08-31 03:00",
			),
			policy: Policy{Daily: 2, Monthly: 3},
			want: []string{
				"2026-10-10 03:00", "2026-10-09 03:00",
				"2026-09-30 03:00", "2026-08-31 03:00",
			},
		},
		{
			name:   "newest is always kept even when no rule would pick it",
			items:  items("2026-10-10 03:00", "2026-09-30 03:00", "2026-08-31 03:00"),
			policy: Policy{Monthly: 1},
			want:   []string{"2026-10-10 03:00"},
		},
		{
			name:   "input order does not matter",
			items:  items("2026-10-01 03:00", "2026-10-03 03:00", "2026-10-02 03:00"),
			policy: Policy{Daily: 2},
			want:   []string{"2026-10-03 03:00", "2026-10-02 03:00"},
		},
		{
			name:   "limit larger than history keeps everything",
			items:  items("2026-10-03 03:00", "2026-10-02 03:00"),
			policy: Policy{Daily: 30, Weekly: 10, Monthly: 12},
			want:   []string{"2026-10-03 03:00", "2026-10-02 03:00"},
		},
		{
			name:   "same timestamp is ordered by id and both fall in one bucket",
			items:  []Item{{ID: "a", Time: at("2026-10-03 03:00")}, {ID: "b", Time: at("2026-10-03 03:00")}},
			policy: Policy{Daily: 5},
			want:   []string{"b"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ds, err := Plan(tc.items, tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(ds) != len(tc.items) {
				t.Fatalf("got %d decisions for %d items", len(ds), len(tc.items))
			}
			if got := kept(ds); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("kept %v\nwant %v", got, tc.want)
			}
			for _, d := range ds {
				if d.Keep && len(d.Reasons) == 0 {
					t.Errorf("%s kept without a reason", d.ID)
				}
				if !d.Keep && len(d.Reasons) != 0 {
					t.Errorf("%s dropped but has reasons %v", d.ID, d.Reasons)
				}
			}
		})
	}
}

func TestPlanUsesUTCBuckets(t *testing.T) {
	// 23:30 UTC on Oct 3 is already Oct 4 in WIB (UTC+7), but buckets are UTC.
	wib := time.FixedZone("WIB", 7*3600)
	a := Item{ID: "a", Time: time.Date(2026, 10, 4, 6, 30, 0, 0, wib)}
	b := Item{ID: "b", Time: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	ds, err := Plan([]Item{a, b}, Policy{Daily: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := kept(ds); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("kept %v, want only a (both fall on 2026-10-03 UTC)", got)
	}
}

func TestPlanReasons(t *testing.T) {
	ds, err := Plan(items("2026-10-03 03:00", "2026-10-02 03:00"), Policy{Daily: 2, Monthly: 1})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(ds[0].Reasons, ", ")
	want := "newest, daily 2026-10-03, monthly 2026-10"
	if got != want {
		t.Errorf("reasons = %q, want %q", got, want)
	}
}

func TestPolicyValidate(t *testing.T) {
	for _, p := range []Policy{{}, {Daily: -1, Weekly: 2}, {Monthly: -3}} {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v should be rejected", p)
		}
		if _, err := Plan(nil, p); err == nil {
			t.Errorf("Plan should reject %+v", p)
		}
	}
	if err := (Policy{Weekly: 1}).Validate(); err != nil {
		t.Errorf("weekly-only policy rejected: %v", err)
	}
}

func BenchmarkPlan(b *testing.B) {
	var its []Item
	base := at("2026-01-01 03:00")
	for i := 0; i < 2000; i++ {
		its = append(its, Item{ID: fmt.Sprint(i), Time: base.Add(time.Duration(i) * 6 * time.Hour)})
	}
	for i := 0; i < b.N; i++ {
		if _, err := Plan(its, Policy{Daily: 7, Weekly: 4, Monthly: 6}); err != nil {
			b.Fatal(err)
		}
	}
}
