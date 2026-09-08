package main

import (
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
)

// The golden is the PICTURE, not the file.
//
// This used to hash the PNG's bytes, which made it a test of Go's image/png
// encoder as much as of this renderer. That encoder's compression choices are
// not part of Go's compatibility promise, and they changed between 1.26 and
// 1.27: on 2026-09-08 the same TOWN title screen encoded to
// 4746ad6f9276c665... under go1.26.7, which CI pins, and to
// b8c216f1dae48642... under go1.27.1. So CI was green while everyone
// developing on a current Go saw a red suite -- which is how a team learns to
// ignore red.
//
// Both decode to the same 480x350 pixels, checked under both toolchains before
// this was written down. So the fixture below is the hash of the decoded image
// rather than of the file, which is what the test was always trying to say:
// this world, drawn by this renderer, looks like this. Nothing about the
// rendering changed, and no golden was re-recorded to make a failure go away --
// the picture is the same picture it has always been.
func TestTownTitleGolden(t *testing.T) {
	// Read the committed fixture (byte-identical to any engine-dir copy), not an
	// untracked world, so the golden runs and fails closed in a clean clone
	// (task M16.1).
	world := filepath.Join("..", "..", "..", "fixtures", "TOWN.ZZT")
	if _, err := os.Stat(world); err != nil {
		t.Fatalf("required fixture %s is missing: %v (it is committed; do not skip past it)", world, err)
	}
	out := filepath.Join(t.TempDir(), "town-title.png")
	if err := shot(world, out, 0); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, format, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decoding the shot: %v", err)
	}
	if format != "png" {
		t.Fatalf("shot wrote a %q, want png", format)
	}

	bounds := img.Bounds()
	const wantW, wantH = 480, 350
	if bounds.Dx() != wantW || bounds.Dy() != wantH {
		t.Fatalf("shot is %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), wantW, wantH)
	}

	// The dimensions go in first, so an image of the right pixels at the wrong
	// shape cannot hash to the same thing as one of the right shape.
	sum := sha256.New()
	fmt.Fprintf(sum, "%dx%d\n", bounds.Dx(), bounds.Dy())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			fmt.Fprintf(sum, "%04x%04x%04x%04x", r, g, b, a)
		}
	}

	const want = "877fd5898c17d96880abf47cf17b3e36baae60d423463b1d5f1a3478bc851704"
	if got := fmt.Sprintf("%x", sum.Sum(nil)); got != want {
		t.Fatalf("TOWN title pixels = %s, want %s", got, want)
	}
}
