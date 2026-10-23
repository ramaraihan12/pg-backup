// Package retention decides which backups to keep under a grandfather-father-son policy.
//
// Days, ISO weeks and months are counted in UTC. For each rule the newest
// backup of a period is the one kept, and only periods that actually contain a
// backup count. So "daily: 7" means the newest backup of each of the last 7
// days that have one, which is how restic's keep-daily works too. A backup is
// kept when any rule keeps it. The newest backup is always kept.
package retention

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Policy says how many periods of each kind to keep.
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// Validate rejects negative counts and a policy that keeps nothing.
func (p Policy) Validate() error {
	if p.Daily < 0 || p.Weekly < 0 || p.Monthly < 0 {
		return errors.New("retention counts cannot be negative")
	}
	if p.Daily == 0 && p.Weekly == 0 && p.Monthly == 0 {
		return errors.New("retention keeps nothing: set at least one of daily, weekly, monthly")
	}
	return nil
}

// Item is one backup.
type Item struct {
	ID   string
	Time time.Time
}

// Decision is the outcome for one backup.
type Decision struct {
	Item
	Keep    bool
	Reasons []string
}

type rule struct {
	name   string
	limit  int
	bucket func(time.Time) string
	kept   int
	last   string
}

// Plan returns one decision per item, newest first.
func Plan(items []Item, p Policy) ([]Decision, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	sorted := append([]Item(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Time.Equal(sorted[j].Time) {
			return sorted[i].Time.After(sorted[j].Time)
		}
		return sorted[i].ID > sorted[j].ID
	})

	rules := []*rule{
		{name: "daily", limit: p.Daily, bucket: func(t time.Time) string { return t.Format("2006-01-02") }},
		{name: "weekly", limit: p.Weekly, bucket: func(t time.Time) string {
			y, w := t.ISOWeek()
			return fmt.Sprintf("%d-W%02d", y, w)
		}},
		{name: "monthly", limit: p.Monthly, bucket: func(t time.Time) string { return t.Format("2006-01") }},
	}

	out := make([]Decision, 0, len(sorted))
	for i, it := range sorted {
		d := Decision{Item: it}
		if i == 0 {
			d.Keep = true
			d.Reasons = append(d.Reasons, "newest")
		}
		t := it.Time.UTC()
		for _, r := range rules {
			if r.limit == 0 || r.kept >= r.limit {
				continue
			}
			b := r.bucket(t)
			if b == r.last {
				continue
			}
			r.last = b
			r.kept++
			d.Keep = true
			d.Reasons = append(d.Reasons, r.name+" "+b)
		}
		out = append(out, d)
	}
	return out, nil
}
