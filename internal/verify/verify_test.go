package verify

import (
	"strings"
	"testing"

	"github.com/ramaraihan12/pg-backup/internal/manifest"
)

func tables(kv ...any) []manifest.Table {
	var out []manifest.Table
	for i := 0; i < len(kv); i += 2 {
		out = append(out, manifest.Table{Schema: "public", Name: kv[i].(string), Rows: int64(kv[i+1].(int))})
	}
	return out
}

func TestCompare(t *testing.T) {
	want := tables("customers", 10, "orders", 20, "empty", 0)
	got := tables("customers", 10, "orders", 19, "surprise", 5)

	res := compare(want, got)
	byName := map[string]TableResult{}
	for _, r := range res {
		byName[r.Table] = r
	}
	if len(res) != 4 {
		t.Fatalf("got %d results: %+v", len(res), res)
	}
	if !byName["public.customers"].OK {
		t.Error("equal counts should pass")
	}
	if r := byName["public.orders"]; r.OK || !strings.Contains(r.Note, "expected 20 rows, restored 19") {
		t.Errorf("orders: %+v", r)
	}
	if r := byName["public.empty"]; r.OK || r.Note != "missing after restore" {
		t.Errorf("a table that is gone must fail even if it was empty: %+v", r)
	}
	if r := byName["public.surprise"]; r.OK || r.Note != "not in the manifest" {
		t.Errorf("surprise: %+v", r)
	}
}

func TestCompareEmptyTableThatSurvives(t *testing.T) {
	res := compare(tables("empty", 0), tables("empty", 0))
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("%+v", res)
	}
}

func TestScratchNameIsValidAndUnique(t *testing.T) {
	a, b := scratchName("20261023T031500Z"), scratchName("20261023T031500Z")
	if a == b {
		t.Error("two drills of the same backup must not collide")
	}
	if !strings.HasPrefix(a, "pgbackup_verify_20261023t031500z_") {
		t.Errorf("name = %s", a)
	}
	if len(a) > 63 {
		t.Errorf("%s is longer than Postgres allows (63)", a)
	}
}
