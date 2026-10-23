// Package demo generates a made-up Indonesian shop database ("toko_maju")
// so backups have something realistic to chew on. Every name, address, phone
// number and price is invented. Output is deterministic for a given seed.
package demo

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
)

// Params sizes the dataset. Rows in order_items are about 3x Orders.
type Params struct {
	Customers int
	Products  int
	Orders    int
	Seed      uint64
}

// Default is a few million rows in total.
func Default() Params {
	return Params{Customers: 250_000, Products: 2_000, Orders: 1_000_000, Seed: 1}
}

// Small is for tests.
func Small() Params {
	return Params{Customers: 200, Products: 40, Orders: 600, Seed: 1}
}

const schema = `
CREATE TABLE customers (
    id         bigint PRIMARY KEY,
    name       text NOT NULL,
    email      text NOT NULL,
    phone      text NOT NULL,
    city       text NOT NULL,
    province   text NOT NULL,
    joined_at  timestamptz NOT NULL
);
CREATE TABLE products (
    id         integer PRIMARY KEY,
    sku        text NOT NULL UNIQUE,
    name       text NOT NULL,
    category   text NOT NULL,
    price_idr  bigint NOT NULL CHECK (price_idr > 0),
    stock      integer NOT NULL
);
CREATE TABLE orders (
    id               bigint PRIMARY KEY,
    customer_id      bigint NOT NULL,
    status           text NOT NULL,
    ordered_at       timestamptz NOT NULL,
    payment_method   text NOT NULL,
    shipping_address text NOT NULL,
    note             text NOT NULL,
    total_idr        bigint NOT NULL
);
CREATE TABLE order_items (
    id             bigint PRIMARY KEY,
    order_id       bigint NOT NULL,
    product_id     integer NOT NULL,
    qty            integer NOT NULL CHECK (qty > 0),
    unit_price_idr bigint NOT NULL
);`

const constraints = `
ALTER TABLE orders ADD CONSTRAINT orders_customer_fk FOREIGN KEY (customer_id) REFERENCES customers (id);
ALTER TABLE order_items ADD CONSTRAINT order_items_order_fk FOREIGN KEY (order_id) REFERENCES orders (id);
ALTER TABLE order_items ADD CONSTRAINT order_items_product_fk FOREIGN KEY (product_id) REFERENCES products (id);
CREATE INDEX orders_customer_idx ON orders (customer_id);
CREATE INDEX orders_ordered_at_idx ON orders (ordered_at);
CREATE INDEX order_items_order_idx ON order_items (order_id);
CREATE INDEX order_items_product_idx ON order_items (product_id);
ANALYZE;`

// Reset drops the demo tables if they exist.
func Reset(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, "DROP TABLE IF EXISTS order_items, orders, products, customers")
	return err
}

// Generate creates the tables and fills them. The tables must not exist.
func Generate(ctx context.Context, conn *pgx.Conn, p Params, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	if _, err := conn.Exec(ctx, schema); err != nil {
		return fmt.Errorf("create tables (use --reset to drop old ones): %w", err)
	}
	products := makeProducts(p)

	progress(fmt.Sprintf("customers: %d", p.Customers))
	n := 0
	_, err := conn.CopyFrom(ctx, pgx.Identifier{"customers"},
		[]string{"id", "name", "email", "phone", "city", "province", "joined_at"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if n >= p.Customers {
				return nil, nil
			}
			n++
			c := makeCustomer(p, int64(n))
			return []any{c.id, c.name, c.email, c.phone, c.city, c.province, c.joinedAt}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy customers: %w", err)
	}

	progress(fmt.Sprintf("products: %d", p.Products))
	i := 0
	_, err = conn.CopyFrom(ctx, pgx.Identifier{"products"},
		[]string{"id", "sku", "name", "category", "price_idr", "stock"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if i >= len(products) {
				return nil, nil
			}
			pr := products[i]
			i++
			return []any{pr.id, pr.sku, pr.name, pr.category, pr.price, pr.stock}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy products: %w", err)
	}

	progress(fmt.Sprintf("orders: %d", p.Orders))
	n = 0
	_, err = conn.CopyFrom(ctx, pgx.Identifier{"orders"},
		[]string{"id", "customer_id", "status", "ordered_at", "payment_method", "shipping_address", "note", "total_idr"},
		pgx.CopyFromFunc(func() ([]any, error) {
			if n >= p.Orders {
				return nil, nil
			}
			n++
			o := makeOrder(p, products, int64(n))
			return []any{o.id, o.customerID, o.status, o.orderedAt, o.payment, o.address, o.note, o.total()}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy orders: %w", err)
	}

	progress("order_items")
	n = 0
	var cur order
	k := 0
	var itemID int64
	_, err = conn.CopyFrom(ctx, pgx.Identifier{"order_items"},
		[]string{"id", "order_id", "product_id", "qty", "unit_price_idr"},
		pgx.CopyFromFunc(func() ([]any, error) {
			for k >= len(cur.items) {
				if n >= p.Orders {
					return nil, nil
				}
				n++
				cur = makeOrder(p, products, int64(n))
				k = 0
			}
			it := cur.items[k]
			k++
			itemID++
			return []any{itemID, cur.id, it.productID, it.qty, it.price}, nil
		}))
	if err != nil {
		return fmt.Errorf("copy order_items: %w", err)
	}

	progress("constraints and indexes")
	if _, err := conn.Exec(ctx, constraints); err != nil {
		return fmt.Errorf("add constraints: %w", err)
	}
	return nil
}

// ---- deterministic data -------------------------------------------------

// The shop's "now": data runs for two years up to this date.
var endDate = time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)

const span = 2 * 365 * 24 * time.Hour

func rng(seed uint64, stream uint64, id uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed^(stream*0x9E3779B97F4A7C15), id))
}

func pick[T any](r *rand.Rand, xs []T) T { return xs[r.IntN(len(xs))] }

type customer struct {
	id                                 int64
	name, email, phone, city, province string
	joinedAt                           time.Time
}

func makeCustomer(p Params, id int64) customer {
	r := rng(p.Seed, 1, uint64(id))
	first, last := pick(r, firstNames), pick(r, lastNames)
	c := pick(r, cities)
	return customer{
		id:       id,
		name:     first + " " + last,
		email:    fmt.Sprintf("%s.%s%d@contoh.test", lower(first), lower(last), id),
		phone:    fmt.Sprintf("+62 8%02d-%04d-%04d", 11+r.IntN(80), r.IntN(10000), r.IntN(10000)),
		city:     c.name,
		province: c.province,
		joinedAt: endDate.Add(-time.Duration(r.Int64N(int64(span)))).Truncate(time.Second),
	}
}

type product struct {
	id       int
	sku      string
	name     string
	category string
	price    int64
	stock    int
}

func makeProducts(p Params) []product {
	r := rng(p.Seed, 2, 0)
	out := make([]product, p.Products)
	for i := range out {
		base := pick(r, productBases)
		size := pick(r, base.sizes)
		out[i] = product{
			id:       i + 1,
			sku:      fmt.Sprintf("TM-%05d", i+1),
			name:     fmt.Sprintf("%s %s (%s)", base.name, size, pick(r, variants)),
			category: base.category,
			price:    int64(base.price/500+int64(r.IntN(40)))*500 + 500, // rounded to Rp 500
			stock:    r.IntN(900),
		}
	}
	return out
}

type item struct {
	productID int
	qty       int
	price     int64
}

type order struct {
	id         int64
	customerID int64
	status     string
	orderedAt  time.Time
	payment    string
	address    string
	note       string
	items      []item
}

func (o order) total() int64 {
	var t int64
	for _, it := range o.items {
		t += int64(it.qty) * it.price
	}
	return t
}

func makeOrder(p Params, products []product, id int64) order {
	r := rng(p.Seed, 3, uint64(id))
	o := order{
		id:         id,
		customerID: 1 + r.Int64N(int64(p.Customers)),
		status:     pickWeighted(r, statuses),
		orderedAt:  endDate.Add(-time.Duration(r.Int64N(int64(span)))).Truncate(time.Second),
		payment:    pick(r, payments),
	}
	c := pick(r, cities)
	o.address = fmt.Sprintf("Jl. %s No. %d, RT %02d/RW %02d, Kel. %s, %s, %s",
		pick(r, streets), 1+r.IntN(250), 1+r.IntN(15), 1+r.IntN(12), pick(r, villages), c.name, c.province)
	if r.IntN(3) == 0 {
		o.note = pick(r, notes)
	}
	n := 1 + r.IntN(5)
	o.items = make([]item, n)
	for i := range o.items {
		pr := products[r.IntN(len(products))]
		o.items[i] = item{productID: pr.id, qty: 1 + r.IntN(4), price: pr.price}
	}
	return o
}

func pickWeighted(r *rand.Rand, ws []weighted) string {
	total := 0
	for _, w := range ws {
		total += w.weight
	}
	n := r.IntN(total)
	for _, w := range ws {
		if n < w.weight {
			return w.value
		}
		n -= w.weight
	}
	return ws[len(ws)-1].value
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
