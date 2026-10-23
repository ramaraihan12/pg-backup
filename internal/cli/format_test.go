package cli

import (
	"testing"
	"time"
)

func TestHuman(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536: "1.5 KiB",
		5 << 20: "5.0 MiB", 3 << 30: "3.0 GiB",
	} {
		if got := human(in); got != want {
			t.Errorf("human(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestThousands(t *testing.T) {
	for in, want := range map[int64]string{
		0: "0", 999: "999", 1000: "1,000", 3412000: "3,412,000", -1234567: "-1,234,567",
	} {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSeconds(t *testing.T) {
	if got := seconds(250 * time.Millisecond); got != "250ms" {
		t.Error(got)
	}
	if got := seconds(41234 * time.Millisecond); got != "41.2s" {
		t.Error(got)
	}
}
