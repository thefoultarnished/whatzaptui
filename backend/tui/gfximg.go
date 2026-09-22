package main

// Full image rendering via terminal graphics protocols.
//
// The default "pixel" media view renders images as half-block ANSI art.
// The "full" media view instead sends the image to the terminal itself:
//   - Kitty graphics protocol (persistent image IDs: upload once, cheap
//     per-frame placement; placements are deleted and re-issued every
//     frame because Bubble Tea repaints the whole screen).
//   - iTerm2 inline images (TERM_PROGRAM=iTerm.app or WezTerm; the image
//     is retransmitted every frame from a small encoded cache).
//
// Terminals without support fall back to pixel art. All escape sequences
// are zero-width for layout purposes; see stripGraphicsSeqs.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
)

type gfxProto int

const (
	gfxNone gfxProto = iota
	gfxKitty
	gfxIterm
)

const (
	// Display bounds for full renders (terminal cells / rows).
	maxGfxCols = 60
	maxGfxRows = 30
	// Kitty uploads at most this many persistent images before evicting.
	maxGfxUploaded = 50
	// iTerm2 encoded payloads cached (retransmitted every frame).
	maxGfxEncCache = 20
	// Kitty control payloads larger than this are chunked.
	kittyChunkSize = 4096
)

func (p gfxProto) String() string {
	switch p {
	case gfxKitty:
		return "kitty"
	case gfxIterm:
		return "iterm"
	default:
		return "none"
	}
}

func detectGraphicsProto() gfxProto {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return gfxKitty
	}
	if strings.Contains(os.Getenv("TERM"), "kitty") {
		return gfxKitty
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm":
		return gfxIterm
	}
	return gfxNone
}

// inlineMediaArt reports whether the media view renders image content
// inline (pixel art or full terminal graphics) as opposed to a tag.
func inlineMediaArt() bool {
	return currentConfig.MediaViewStyle == "pixel" || currentConfig.MediaViewStyle == "full"
}

type gfxState struct {
	proto    gfxProto
	uploaded map[uint32]string // kitty image id -> msg key, FIFO via order
	order    []uint32
	prev     []string // msg keys placed last frame (kitty: re-issue deletes)
	enc      map[string]string
	encOrder []string
}

func newGfxState() *gfxState {
	return &gfxState{
		proto:    detectGraphicsProto(),
		uploaded: map[uint32]string{},
		enc:      map[string]string{},
	}
}

// gfxID derives a stable Kitty image id from a message key.
func gfxID(msgKey string) uint32 {
	return crc32.ChecksumIEEE([]byte(msgKey))
}

// wrapTmux wraps an escape sequence for tmux passthrough.
func wrapTmux(seq string) string {
	if os.Getenv("TMUX") == "" {
		return seq
	}
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

func kittyChunk(payload, data string, last bool) string {
	m := "1"
	if last {
		m = "0"
	}
	if payload == "" {
		return wrapTmux("\x1b_Gm=" + m + ";" + data + "\x1b\\")
	}
	return wrapTmux("\x1b_G" + payload + ",m=" + m + ";" + data + "\x1b\\")
}

// kittyTransmit uploads PNG data once under id, then places it.
func kittyTransmit(id uint32, pngData []byte, cols, rows int) string {
	enc := base64.StdEncoding.EncodeToString(pngData)
	var sb strings.Builder
	head := fmt.Sprintf("a=t,f=100,i=%d,q=2", id)
	if len(enc) <= kittyChunkSize {
		sb.WriteString(kittyChunk(head, enc, true))
	} else {
		sb.WriteString(kittyChunk(head, enc[:kittyChunkSize], false))
		rest := enc[kittyChunkSize:]
		for len(rest) > kittyChunkSize {
			sb.WriteString(kittyChunk("", rest[:kittyChunkSize], false))
			rest = rest[kittyChunkSize:]
		}
		sb.WriteString(kittyChunk("", rest, true))
	}
	sb.WriteString(kittyPlace(id, cols, rows))
	return sb.String()
}

func kittyPlace(id uint32, cols, rows int) string {
	return wrapTmux(fmt.Sprintf("\x1b_Ga=p,i=%d,c=%d,r=%d,q=2\x1b\\", id, cols, rows))
}

func kittyDeletePlacement(id uint32) string {
	return wrapTmux(fmt.Sprintf("\x1b_Ga=d,d=p,i=%d,q=2\x1b\\", id))
}

func kittyDeleteImage(id uint32) string {
	return wrapTmux(fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id))
}

func itermFile(pngData []byte, cols, rows int) string {
	return wrapTmux("\x1b]1337;File=inline=1;width=" + strconv.Itoa(cols) + "cells;height=" + strconv.Itoa(rows) + "cells;preserveAspectRatio=1:" +
		base64.StdEncoding.EncodeToString(pngData) + "\x07")
}

// takePrevDeletes returns Kitty placement deletes for the previous frame
// and clears the list. Must be called once per View, before rendering.
func (st *gfxState) takePrevDeletes() string {
	if st == nil || st.proto != gfxKitty || len(st.prev) == 0 {
		if st != nil {
			st.prev = nil
		}
		return ""
	}
	var sb strings.Builder
	for _, key := range st.prev {
		sb.WriteString(kittyDeletePlacement(gfxID(key)))
	}
	st.prev = nil
	return sb.String()
}

// scaleForCells downscales src to fit cols terminal columns, returning the
// RGBA pixels and the number of text rows the image occupies. Terminal
// cells are roughly twice as tall as wide.
func scaleForCells(src image.Image, cols int) (*image.RGBA, int) {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if cols > maxGfxCols {
		cols = maxGfxCols
	}
	if cols < 10 {
		cols = 10
	}
	rows := 2
	if w > 0 && h > 0 {
		rows = int(float64(cols)*2*float64(h)/float64(w) + 0.5)
	}
	if rows < 2 {
		rows = 2
	}
	if rows > maxGfxRows {
		rows = maxGfxRows
	}
	dst := image.NewRGBA(image.Rect(0, 0, cols, rows))
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			dst.Set(x, y, src.At(b.Min.X+x*w/cols, b.Min.Y+y*h/rows))
		}
	}
	return dst, rows
}

func encodePNG(img image.Image) ([]byte, bool) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// fullImageLines renders a downloaded image as terminal graphics, returning
// display lines (escape + space padding on the first row, spaces after).
// ok=false means fall back to pixel art.
func (x *m) fullImageLines(msg wireMsg, availableW int) ([]string, bool) {
	if x.gfx == nil || x.gfx.proto == gfxNone {
		return nil, false
	}
	localPath := x.downloadedMedia[msg.Key.ID]
	if localPath == "" {
		return nil, false
	}
	f, err := os.Open(localPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, false
	}
	cols := availableW
	if cols > maxGfxCols {
		cols = maxGfxCols
	}
	if cols < 10 {
		return nil, false
	}
	scaled, rows := scaleForCells(src, cols)
	data, ok := encodePNG(scaled)
	if !ok {
		return nil, false
	}
	key := msg.Key.ID
	var esc string
	switch x.gfx.proto {
	case gfxKitty:
		id := gfxID(key)
		if _, known := x.gfx.uploaded[id]; !known {
			for len(x.gfx.order) >= maxGfxUploaded {
				old := x.gfx.order[0]
				x.gfx.order = x.gfx.order[1:]
				delete(x.gfx.uploaded, old)
				esc += kittyDeleteImage(old)
			}
			x.gfx.uploaded[id] = key
			x.gfx.order = append(x.gfx.order, id)
			esc += kittyTransmit(id, data, cols, rows)
		} else {
			esc += kittyPlace(id, cols, rows)
		}
		x.gfx.prev = append(x.gfx.prev, key)
	case gfxIterm:
		cacheKey := key + "/" + strconv.Itoa(cols) + "x" + strconv.Itoa(rows)
		if cached, hit := x.gfx.enc[cacheKey]; hit {
			esc = cached
		} else {
			for len(x.gfx.encOrder) >= maxGfxEncCache {
				old := x.gfx.encOrder[0]
				x.gfx.encOrder = x.gfx.encOrder[1:]
				delete(x.gfx.enc, old)
			}
			esc = itermFile(data, cols, rows)
			x.gfx.enc[cacheKey] = esc
			x.gfx.encOrder = append(x.gfx.encOrder, cacheKey)
		}
	default:
		return nil, false
	}
	lines := make([]string, 0, rows)
	lines = append(lines, esc+strings.Repeat(" ", cols))
	for i := 1; i < rows; i++ {
		lines = append(lines, strings.Repeat(" ", cols))
	}
	return lines, true
}

// gfxName reports the terminal graphics protocol for the loading screen.
func (x m) gfxName() string {
	if x.gfx == nil {
		return detectGraphicsProto().String()
	}
	return x.gfx.proto.String()
}

// stripGraphicsSeqs removes terminal-graphics escapes (Kitty APC, iTerm2
// OSC) for width measurement. Plain lines are returned untouched.
func stripGraphicsSeqs(s string) string {
	if !strings.Contains(s, "\x1b_G") && !strings.Contains(s, "]1337;") {
		return s
	}
	var sb strings.Builder
	for {
		gi := strings.Index(s, "\x1b_G")
		oi := strings.Index(s, "\x1b]1337;")
		idx := -1
		isKitty := false
		if gi >= 0 && (oi < 0 || gi < oi) {
			idx, isKitty = gi, true
		} else if oi >= 0 {
			idx = oi
		} else {
			sb.WriteString(s)
			break
		}
		sb.WriteString(s[:idx])
		rest := s[idx:]
		if isKitty {
			end := strings.Index(rest, "\x1b\\")
			if end < 0 {
				break
			}
			s = rest[end+2:]
		} else {
			end := strings.Index(rest, "\x07")
			if end < 0 {
				break
			}
			s = rest[end+1:]
		}
	}
	return sb.String()
}
