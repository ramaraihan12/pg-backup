package store

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	if _, err := m.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get of a missing key = %v, want ErrNotFound", err)
	}

	for _, k := range []string{"a/2", "a/1", "b/1"} {
		if err := m.Put(ctx, k, strings.NewReader("body "+k)); err != nil {
			t.Fatal(err)
		}
	}
	rc, err := m.Get(ctx, "a/1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "body a/1" {
		t.Errorf("body = %q", b)
	}

	list, err := m.List(ctx, "a/")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Key != "a/1" || list[1].Key != "a/2" || list[0].Size != 8 {
		t.Errorf("list = %+v", list)
	}

	if err := m.Delete(ctx, "a/1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, "a/1"); err != nil {
		t.Errorf("deleting twice should be fine: %v", err)
	}
	if list, _ := m.List(ctx, "a/"); len(list) != 1 {
		t.Errorf("list after delete = %+v", list)
	}
}
