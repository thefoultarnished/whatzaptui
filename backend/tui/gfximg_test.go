package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 128, 255})
		}
	}
	p := filepath.Join(t.TempDir(), "img.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDetectGraphicsProto(t *testing.T) {
	if gfxKitty.String() != "kitty" || gfxIterm.String() != "iterm" || gfxNone.String() != "none" {
		t.Fatal("bad proto names")
	}
	t.Setenv("KITTY_WINDOW_ID", "1")
	if detectGraphicsProto() != gfxKitty {
		t.Fatal("want kitty")
	}
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM", "xterm-kitty")
	if detectGraphicsProto() != gfxKitty {
		t.Fatal("want kitty via TERM")
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if detectGraphicsProto() != gfxIterm {
		t.Fatal("want iterm")
	}
	t.Setenv("TERM_PROGRAM", "WezTerm")
	if detectGraphicsProto() != gfxIterm {
		t.Fatal("want iterm for WezTerm")
	}
	t.Setenv("TERM_PROGRAM", "")
	if detectGraphicsProto() != gfxNone {
		t.Fatal("want none")
	}
}

func TestScaleForCellsRows(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	_, rows := scaleForCells(src, 20)
	// cells ~2:1, so 20 cols x (20*2*50/100=20 rows)
	if rows != 20 {
		t.Fatalf("rows = %d, want 20", rows)
	}
	_, rows = scaleForCells(src, 200)
	if rows != maxGfxRows {
		t.Fatalf("rows = %d, want cap %d", rows, maxGfxRows)
	}
}

func TestFullImageKittyTransmitAndPlace(t *testing.T) {
	p := writeTestPNG(t, 80, 40)
	x := m{
		downloadedMedia: map[string]string{"m1": p},
		gfx:             &gfxState{proto: gfxKitty, uploaded: map[uint32]string{}, enc: map[string]string{}},
	}
	msg := mkMsg("m1", 1)
	lines, ok := x.fullImageLines(msg, 40)
	if !ok {
		t.Fatal("want ok")
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "\x1b_G") {
		t.Fatal("first line must carry Kitty escape")
	}
	if !strings.Contains(lines[0], "a=t") {
		t.Fatal("first render must transmit")
	}
	// Second frame: placement only, no retransmit.
	x.gfx.prev = nil
	lines2, ok := x.fullImageLines(msg, 40)
	if !ok {
		t.Fatal("want ok")
	}
	if strings.Contains(lines2[0], "a=t") {
		t.Fatal("second render must reuse upload (placement only)")
	}
	if !strings.Contains(lines2[0], "a=p") {
		t.Fatal("want placement escape")
	}
	dels := x.gfx.takePrevDeletes()
	if !strings.Contains(dels, "a=d") {
		t.Fatal("want placement deletes")
	}
	if len(x.gfx.prev) != 0 {
		t.Fatal("prev must clear after take")
	}
}

func TestFullImageItermRetransmits(t *testing.T) {
	p := writeTestPNG(t, 80, 40)
	x := m{
		downloadedMedia: map[string]string{"m1": p},
		gfx:             &gfxState{proto: gfxIterm, uploaded: map[uint32]string{}, enc: map[string]string{}},
	}
	msg := mkMsg("m1", 1)
	lines, ok := x.fullImageLines(msg, 40)
	if !ok || !strings.Contains(lines[0], "]1337;File=inline=1") {
		t.Fatal("want iTerm inline escape")
	}
	if len(x.gfx.enc) != 1 {
		t.Fatal("want encoded payload cached")
	}
}

func TestFullImageFallbacks(t *testing.T) {
	msg := mkMsg("m1", 1)
	// No gfx state.
	if _, ok := ((&m{}).fullImageLines(msg, 40)); ok {
		t.Fatal("nil gfx must fall back")
	}
	// Unsupported terminal.
	x := m{gfx: &gfxState{proto: gfxNone}}
	if _, ok := x.fullImageLines(msg, 40); ok {
		t.Fatal("gfxNone must fall back")
	}
	// Missing file.
	x = m{
		downloadedMedia: map[string]string{"m1": "nope.png"},
		gfx:             &gfxState{proto: gfxKitty, uploaded: map[uint32]string{}, enc: map[string]string{}},
	}
	if _, ok := x.fullImageLines(msg, 40); ok {
		t.Fatal("missing file must fall back")
	}
}

func TestStripGraphicsSeqs(t *testing.T) {
	plain := "hello world"
	if stripGraphicsSeqs(plain) != plain {
		t.Fatal("plain lines untouched")
	}
	kitty := "ab\x1b_Ga=p,i=1,q=2\x1b\\cd"
	if got := stripGraphicsSeqs(kitty); got != "abcd" {
		t.Fatalf("kitty strip = %q", got)
	}
	iterm := "ab\x1b]1337;File=inline=1:AAAA\x07cd"
	if got := stripGraphicsSeqs(iterm); got != "abcd" {
		t.Fatalf("iterm strip = %q", got)
	}
}

func TestMediaViewPickerHasFull(t *testing.T) {
	found := false
	for _, it := range buildMediaViewPickerItems() {
		if it.key == "full" {
			found = true
		}
	}
	if !found {
		t.Fatal("media view picker missing full option")
	}
	prev := currentConfig.MediaViewStyle
	currentConfig.MediaViewStyle = "full"
	if !inlineMediaArt() {
		t.Fatal("inlineMediaArt must cover full")
	}
	currentConfig.MediaViewStyle = prev
}
