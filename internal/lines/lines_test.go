package lines

import (
	"slices"
	"testing"
)

func TestWriter(t *testing.T) {
	var got []string
	w := New(func(l []byte) { got = append(got, string(l)) })
	for _, chunk := range []string{"one\ntw", "o\r\n", "", "\nthree\nfou", "r"} {
		if n, err := w.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if want := []string{"one", "two", "", "three"}; !slices.Equal(got, want) {
		t.Errorf("before Flush: got %q, want %q", got, want)
	}
	w.Flush()
	w.Flush() // nothing left
	if want := []string{"one", "two", "", "three", "four"}; !slices.Equal(got, want) {
		t.Errorf("after Flush: got %q, want %q", got, want)
	}
}
