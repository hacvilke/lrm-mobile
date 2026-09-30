package main

import (
	"reflect"
	"testing"
)

func TestParseDaemonArgsSplitsOurFlagsFromUpstreams(t *testing.T) {
	o, err := parseDaemonArgs([]string{
		"--supervise", "--port", "9000", "--wake-lock", "--peer", "1.2.3.4:8787",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !o.supervise || !o.wakeLock {
		t.Errorf("our flags were not picked up: %+v", o)
	}
	want := []string{"--port", "9000", "--peer", "1.2.3.4:8787"}
	if !reflect.DeepEqual(o.rest, want) {
		t.Errorf("upstream args = %v, want %v (they must pass through untouched)", o.rest, want)
	}
}

func TestParseDaemonArgsDefaultsAreOff(t *testing.T) {
	o, err := parseDaemonArgs([]string{"--port", "8787"})
	if err != nil {
		t.Fatal(err)
	}
	if o.supervise || o.wakeLock {
		t.Error("supervision and wake locks must be opt-in: they cost battery")
	}
	if o.maxRestarts != 0 {
		t.Errorf("maxRestarts = %d, want 0 (never give up)", o.maxRestarts)
	}
}

func TestParseDaemonArgsMaxRestarts(t *testing.T) {
	o, err := parseDaemonArgs([]string{"--max-restarts", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if o.maxRestarts != 5 {
		t.Errorf("maxRestarts = %d, want 5", o.maxRestarts)
	}
	if _, err := parseDaemonArgs([]string{"--max-restarts"}); err == nil {
		t.Error("a missing value should be an error")
	}
	if _, err := parseDaemonArgs([]string{"--max-restarts", "-3"}); err == nil {
		t.Error("a negative count should be an error")
	}
}

func TestParsePorts(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"22", []int{22}},
		{"22,80,443", []int{22, 80, 443}},
		{"1-5", []int{1, 2, 3, 4, 5}},
		{"80,1-3", []int{80, 1, 2, 3}},
		{"3-1", []int{1, 2, 3}},
		{"22,22,22", []int{22}},
	}
	for _, c := range cases {
		got, err := parsePorts(c.in)
		if err != nil {
			t.Errorf("parsePorts(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parsePorts(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "abc", "0", "70000", "1-70000", "5-", "-"} {
		if _, err := parsePorts(bad); err == nil {
			t.Errorf("parsePorts(%q) should have failed", bad)
		}
	}
}
