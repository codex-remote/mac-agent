package buffer

import (
	"reflect"
	"testing"
)

func TestRingKeepsNewestValues(t *testing.T) {
	ring := New(3)
	for _, value := range []string{"one", "two", "three", "four"} {
		ring.Add(value)
	}
	want := []string{"two", "three", "four"}
	if got := ring.Values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Values() = %#v, want %#v", got, want)
	}
	ring.Reset()
	if got := ring.Values(); len(got) != 0 {
		t.Fatalf("Values() after reset = %#v", got)
	}
}
