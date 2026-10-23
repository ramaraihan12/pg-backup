// Command demodata fills a Postgres database with the made-up "toko_maju"
// shop data used in the README and the tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ramaraihan12/pg-backup/internal/demo"
)

func main() {
	p := demo.Default()
	url := flag.String("url", os.Getenv("DEMO_DATABASE_URL"), "database url (or set DEMO_DATABASE_URL)")
	reset := flag.Bool("reset", false, "drop the demo tables first")
	flag.IntVar(&p.Customers, "customers", p.Customers, "number of customers")
	flag.IntVar(&p.Products, "products", p.Products, "number of products")
	flag.IntVar(&p.Orders, "orders", p.Orders, "number of orders (order_items is about 3x this)")
	flag.Uint64Var(&p.Seed, "seed", p.Seed, "random seed; the same seed gives the same data")
	flag.Parse()

	if *url == "" {
		fmt.Fprintln(os.Stderr, "need -url or DEMO_DATABASE_URL")
		os.Exit(2)
	}
	if err := run(*url, *reset, p); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(url string, reset bool, p demo.Params) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if reset {
		if err := demo.Reset(ctx, conn); err != nil {
			return err
		}
	}
	start := time.Now()
	if err := demo.Generate(ctx, conn, p, func(s string) {
		fmt.Printf("%6.1fs  %s\n", time.Since(start).Seconds(), s)
	}); err != nil {
		return err
	}
	fmt.Printf("%6.1fs  done\n", time.Since(start).Seconds())
	return nil
}
