package dcompress

import (
	"archive/tar"
	"io"
	"os"
	"testing"
)

func TestReader(t *testing.T) {
	testOpenZ(t, "testdata/bsd.pax.Z")
	testOpenZ(t, "testdata/gnu.pax.Z")
}

func testOpenZ(t *testing.T, p string) {
	fd, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}

	r, err := NewReader(fd)
	if err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(r)
	n := 0
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}

		n++

		if len(hdr.Name) == 0 {
			t.Fatal("file name must not be empty")
		}
	}

	if n != 11 {
		t.Fatalf("file count mismatch: %d != 11", n)
	}
}
