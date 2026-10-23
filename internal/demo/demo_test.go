package demo

import (
	"reflect"
	"strings"
	"testing"
)

func TestDeterministic(t *testing.T) {
	p := Small()
	prods := makeProducts(p)
	if !reflect.DeepEqual(prods, makeProducts(p)) {
		t.Error("products differ between runs")
	}
	for _, id := range []int64{1, 17, 600} {
		if !reflect.DeepEqual(makeOrder(p, prods, id), makeOrder(p, prods, id)) {
			t.Errorf("order %d differs between runs", id)
		}
		if !reflect.DeepEqual(makeCustomer(p, id), makeCustomer(p, id)) {
			t.Errorf("customer %d differs between runs", id)
		}
	}
}

func TestSeedChangesData(t *testing.T) {
	a, b := Small(), Small()
	b.Seed = 2
	if reflect.DeepEqual(makeCustomer(a, 5), makeCustomer(b, 5)) {
		t.Error("different seeds should give different customers")
	}
}

func TestOrdersAreConsistent(t *testing.T) {
	p := Small()
	prods := makeProducts(p)
	byID := map[int]product{}
	for _, pr := range prods {
		byID[pr.id] = pr
		if pr.price <= 0 || pr.price%500 != 0 {
			t.Errorf("product %d price %d", pr.id, pr.price)
		}
	}
	items := 0
	for id := int64(1); id <= int64(p.Orders); id++ {
		o := makeOrder(p, prods, id)
		if o.customerID < 1 || o.customerID > int64(p.Customers) {
			t.Fatalf("order %d points at customer %d", id, o.customerID)
		}
		if len(o.items) < 1 || len(o.items) > 5 {
			t.Fatalf("order %d has %d items", id, len(o.items))
		}
		var sum int64
		for _, it := range o.items {
			pr, ok := byID[it.productID]
			if !ok || it.price != pr.price || it.qty < 1 {
				t.Fatalf("order %d item %+v", id, it)
			}
			sum += int64(it.qty) * it.price
		}
		if o.total() != sum || sum <= 0 {
			t.Fatalf("order %d total %d != %d", id, o.total(), sum)
		}
		items += len(o.items)
	}
	if avg := float64(items) / float64(p.Orders); avg < 2.5 || avg > 3.5 {
		t.Errorf("average items per order = %.2f, expected about 3", avg)
	}
}

func TestMadeUpContactDetails(t *testing.T) {
	c := makeCustomer(Small(), 42)
	if !strings.HasSuffix(c.email, "@contoh.test") {
		t.Errorf("email %q should use a reserved test domain", c.email)
	}
	if !strings.HasPrefix(c.phone, "+62 8") {
		t.Errorf("phone %q", c.phone)
	}
}
