package main

import (
	"testing"
)

func TestRangeURLReplacesOnlyRange(t *testing.T) {
	raw := "https://rr1---sn-example.googlevideo.com/videoplayback?expire=1&itag=299&range=1-2&spc=token"
	got, err := rangeURL(raw, 100, 199)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://rr1---sn-example.googlevideo.com/videoplayback?expire=1&itag=299&range=100-199&spc=token"
	if got != want {
		t.Fatalf("rangeURL() = %q, want %q", got, want)
	}
}

func TestSameBrowserRangeURLCanonicalizesQueryOrder(t *testing.T) {
	left := "https://rr1---sn-example.googlevideo.com/videoplayback?itag=299&range=0-9&spc=token"
	right := "https://rr1---sn-example.googlevideo.com/videoplayback?spc=token&range=0-9&itag=299"
	if !sameBrowserRangeURL(left, right) {
		t.Fatal("sameBrowserRangeURL() rejected equivalent query strings")
	}
	if sameBrowserRangeURL(left, "https://rr1---sn-example.googlevideo.com/videoplayback?itag=299&range=10-19&spc=token") {
		t.Fatal("sameBrowserRangeURL() accepted a different range")
	}
}

func TestTargetBrowserMediaURL(t *testing.T) {
	valid := "https://rr1---sn-example.googlevideo.com/videoplayback?expire=1&itag=299&range=0-99"
	for _, test := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "selected adaptive stream", raw: valid, want: true},
		{name: "wrong itag", raw: "https://rr1---sn-example.googlevideo.com/videoplayback?itag=140", want: false},
		{name: "wrong path", raw: "https://rr1---sn-example.googlevideo.com/watch?itag=299", want: false},
		{name: "untrusted host", raw: "https://googlevideo.com.evil.invalid/videoplayback?itag=299", want: false},
		{name: "non media host", raw: "https://youtube.com/videoplayback?itag=299", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isTargetBrowserMediaURL(test.raw, 299); got != test.want {
				t.Fatalf("isTargetBrowserMediaURL(%q) = %t, want %t", test.raw, got, test.want)
			}
		})
	}
}
