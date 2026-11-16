package escpos

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyGoldenMatchesMain5d571a0 pins the legacy GS v 0 path to the
// reference implementation in main@5d571a0. The .golden files were generated
// by that reference (escpos.ParseXML), not by this candidate, so this test
// detects byte drift instead of comparing an implementation with itself.
func TestLegacyGoldenMatchesMain5d571a0(t *testing.T) {
	dir := filepath.Join("testdata", "legacy-main-5d571a0")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".xml") {
			continue
		}
		seen++
		name := strings.TrimSuffix(entry.Name(), ".xml")
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, name+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseXMLWithMode(body, ImageModeLegacy)
			if err != nil {
				t.Fatalf("legacy parse failed: %v", err)
			}
			if !bytes.Equal(got, want) {
				limit := min(len(got), len(want))
				at := -1
				for i := 0; i < limit; i++ {
					if got[i] != want[i] {
						at = i
						break
					}
				}
				t.Fatalf("legacy output differs from main@5d571a0: got %d bytes, want %d bytes, first diff at %d",
					len(got), len(want), at)
			}
		})
	}
	if seen != 7 {
		t.Fatalf("expected 7 legacy fixtures, found %d", seen)
	}
}

// The fixtures must actually cross the legacy slice boundary and produce
// multiple slices; otherwise the golden test would not protect that behavior.
func TestLegacyGoldenFixturesCoverMultipleSlices(t *testing.T) {
	dir := filepath.Join("testdata", "legacy-main-5d571a0")
	sliceHeader := []byte{GS, 0x76, 0x30} // GS v 0
	cases := map[string]int{
		"image-16x2047-below-slice": 1,
		"image-16x2048-exact-slice": 1,
		"image-16x2049-two-slices":  2,
		"image-8x4097-three-slices": 3,
	}
	for name, wantSlices := range cases {
		golden, err := os.ReadFile(filepath.Join(dir, name+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Count(golden, sliceHeader); got != wantSlices {
			t.Fatalf("%s: expected %d legacy slices, found %d", name, wantSlices, got)
		}
	}
}
