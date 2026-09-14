// Command icogen generates the webhook-zq tray icons.
//
// Deliverables (written to -out dir, default D:\webhook-zq\deliver):
//
//	tray-active.ico   solid bolt, #0052d9 (running; matches web header blue)
//	tray-stopped.ico  same shape, #9aa4b2 (stopped)
//
// Each .ico embeds three BMP layers (16/24/32 px, 32bpp BGRA + all-zero AND
// mask) - the most compatible encoding for Windows Shell_NotifyIcon/LoadImage.
//
// Shape: Material Symbols "bolt" polygon (24-unit viewbox), aspect-preserving
// fit into [pad,1-pad]^2, rasterized per size with 4x supersampling AA.
// Both color variants are rasterized from the SAME alpha grid, guaranteeing
// pixel-identical silhouettes.
//
// The program self-verifies: re-parses both .ico files, asserts layer
// specs/offsets, and asserts the two variants share identical alpha channel
// per layer. It also writes preview-tray-icons.png (light & dark taskbar
// simulation + 8x zoom of the 16px layer) for human review.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
)

var sizes = []int{16, 24, 32}

var (
	activeRGB  = [3]uint8{0x00, 0x52, 0xd9} // #0052d9
	stoppedRGB = [3]uint8{0x9a, 0xa4, 0xb2} // #9aa4b2
)

type Pt struct{ X, Y float64 }

// Material Symbols "bolt": M13 3 L4 14 H11 V21 L20 10 H13 Z (24-unit viewbox).
var bolt24 = []Pt{{13, 3}, {4, 14}, {11, 14}, {11, 21}, {20, 10}, {13, 10}}

// fitUnit maps the polygon bounding box into [pad,1-pad]^2, preserving aspect
// ratio and centering.
func fitUnit(pts []Pt, pad float64) []Pt {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY = math.Min(minX, p.X), math.Min(minY, p.Y)
		maxX, maxY = math.Max(maxX, p.X), math.Max(maxY, p.Y)
	}
	sx, sy := maxX-minX, maxY-minY
	s := math.Min((1-2*pad)/sx, (1-2*pad)/sy)
	ox, oy := (1-sx*s)/2, (1-sy*s)/2

	out := make([]Pt, len(pts))
	for i, p := range pts {
		out[i] = Pt{ox + (p.X-minX)*s, oy + (p.Y-minY)*s}
	}
	return out
}

// inPoly is the PNPOLY even-odd point-in-polygon test.
func inPoly(x, y float64, pts []Pt) bool {
	in := false

	j := len(pts) - 1
	for i := 0; i < len(pts); i++ {
		pi, pj := pts[i], pts[j]
		if (pi.Y > y) != (pj.Y > y) {
			t := (y - pi.Y) / (pj.Y - pi.Y)
			if x < pi.X+t*(pj.X-pi.X) {
				in = !in
			}
		}
		j = i
	}
	return in
}

// rasterAlpha rasterizes pts into an n*n coverage grid (0..255) using ss*ss
// subsamples per pixel.
func rasterAlpha(pts []Pt, n, ss int) [][]uint8 {
	grid := make([][]uint8, n)
	for y := 0; y < n; y++ {
		grid[y] = make([]uint8, n)
		for x := 0; x < n; x++ {
			hit := 0

			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					px := (float64(x) + (float64(i)+0.5)/float64(ss)) / float64(n)
					py := (float64(y) + (float64(j)+0.5)/float64(ss)) / float64(n)

					if inPoly(px, py, pts) {
						hit++
					}
				}
			}

			grid[y][x] = uint8(math.Round(float64(hit) / float64(ss*ss) * 255))
		}
	}
	return grid
}

// bmpLayer encodes one icon layer as BITMAPINFOHEADER + bottom-up 32bpp BGRA
// pixels + all-zero AND mask (alpha channel governs transparency).
func bmpLayer(n int, alpha [][]uint8, rgb [3]uint8) []byte {
	pixels := make([]byte, 0, n*n*4)
	for y := n - 1; y >= 0; y-- { // BMP rows are bottom-up
		for x := 0; x < n; x++ {
			pixels = append(pixels, rgb[2], rgb[1], rgb[0], alpha[y][x]) // B,G,R,A
		}
	}
	maskRow := ((n + 31) / 32) * 4
	mask := make([]byte, maskRow*n)

	var h bytes.Buffer
	mustWrite(&h, uint32(40))                    // biSize
	mustWrite(&h, int32(n))                      // biWidth
	mustWrite(&h, int32(2*n))                    // biHeight = XOR + AND
	mustWrite(&h, uint16(1))                     // biPlanes
	mustWrite(&h, uint16(32))                    // biBitCount
	mustWrite(&h, uint32(0))                     // biCompression = BI_RGB
	mustWrite(&h, uint32(len(pixels)+len(mask))) // biSizeImage
	mustWrite(&h, int32(0))                      // biXPelsPerMeter
	mustWrite(&h, int32(0))                      // biYPelsPerMeter
	mustWrite(&h, uint32(0))                     // biClrUsed
	mustWrite(&h, uint32(0))                     // biClrImportant
	h.Write(pixels)
	h.Write(mask)
	return h.Bytes()
}

func buildICO(sizes []int, alphas map[int][][]uint8, rgb [3]uint8) []byte {
	layers := make([][]byte, len(sizes))
	for i, n := range sizes {
		layers[i] = bmpLayer(n, alphas[n], rgb)
	}
	var out bytes.Buffer
	mustWrite(&out, uint16(0))
	mustWrite(&out, uint16(1)) // type = icon
	mustWrite(&out, uint16(len(sizes)))
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		mustWrite(&out, uint8(n))   // width
		mustWrite(&out, uint8(n))   // height
		mustWrite(&out, uint8(0))   // colorCount (0 = truecolor)
		mustWrite(&out, uint8(0))   // reserved
		mustWrite(&out, uint16(1))  // planes
		mustWrite(&out, uint16(32)) // bitCount
		mustWrite(&out, uint32(len(layers[i])))
		mustWrite(&out, uint32(offset))
		offset += len(layers[i])
	}
	for _, l := range layers {
		out.Write(l)
	}
	return out.Bytes()
}

type layer struct {
	n     int
	alpha [][]uint8 // top-down
	rgb   [3]uint8
}

// parseICO decodes an .ico and returns its layers, validating structure.
func parseICO(b []byte) ([]layer, error) {
	if len(b) < 6 {
		return nil, fmt.Errorf("file too short")
	}
	if t := binary.LittleEndian.Uint16(b[2:4]); t != 1 {
		return nil, fmt.Errorf("type = %d, want 1 (icon)", t)
	}
	count := int(binary.LittleEndian.Uint16(b[4:6]))
	if 6+16*count > len(b) {
		return nil, fmt.Errorf("directory overruns file")
	}
	var layers []layer
	for i := 0; i < count; i++ {
		e := 6 + 16*i
		w, h := int(b[e]), int(b[e+1])
		bpp := int(binary.LittleEndian.Uint16(b[e+6 : e+8]))
		size := binary.LittleEndian.Uint32(b[e+8 : e+12])
		off := binary.LittleEndian.Uint32(b[e+12 : e+16])
		if int(off)+int(size) > len(b) {
			return nil, fmt.Errorf("layer %d data overruns file", i)
		}
		if w == 0 || h == 0 || w != h {
			return nil, fmt.Errorf("layer %d: bad size %dx%d", i, w, h)
		}
		if bpp != 32 {
			return nil, fmt.Errorf("layer %d: bitCount = %d, want 32", i, bpp)
		}
		d := b[off : off+size]
		if len(d) < 40 {
			return nil, fmt.Errorf("layer %d: BMP header truncated", i)
		}
		//nosec G115 -- values come from our own generated .ico files, bounded by file format.
		biW := int(int32(binary.LittleEndian.Uint32(d[4:8])))
		//nosec G115 -- same as above: BMP header height, always small.
		biH := int(int32(binary.LittleEndian.Uint32(d[8:12]))) / 2
		hBpp := int(binary.LittleEndian.Uint16(d[14:16]))
		if biW != w || biH != h {
			return nil, fmt.Errorf("layer %d: BMP %dx%d != entry %dx%d", i, biW, biH, w, h)
		}
		if hBpp != 32 {
			return nil, fmt.Errorf("layer %d: BMP bitCount = %d, want 32", i, hBpp)
		}
		pixOff := int(binary.LittleEndian.Uint32(d[0:4]))
		want := pixOff + w*h*4 + ((w+31)/32)*4*h
		if len(d) != want {
			return nil, fmt.Errorf("layer %d: data len %d, want %d", i, len(d), want)
		}
		// Decode alpha (stored bottom-up, BGRA) back to top-down grid.
		alpha := make([][]uint8, h)
		for y := 0; y < h; y++ {
			alpha[y] = make([]uint8, w)
		}
		var rgb [3]uint8
		for row := 0; row < h; row++ {
			src := pixOff + row*w*4
			y := h - 1 - row // first stored row is the bottom one
			for x := 0; x < w; x++ {
				p := src + x*4
				if row == 0 && x == 0 {
					rgb = [3]uint8{d[p+2], d[p+1], d[p]} // R,G,B
				}
				alpha[y][x] = d[p+3]
			}
		}
		layers = append(layers, layer{n: w, alpha: alpha, rgb: rgb})
	}
	return layers, nil
}

// mustWrite writes a binary value in little-endian order, panicking on error.
// bytes.Buffer.Write never errors, so this is safe for the code generator.
func mustWrite(w io.Writer, data any) {
	if err := binary.Write(w, binary.LittleEndian, data); err != nil {
		panic(err)
	}
}

func main() {
	outDir := flag.String("out", `D:\webhook-zq\deliver`, "output directory")
	pad := flag.Float64("pad", 0.07, "shape padding fraction")
	ss := flag.Int("ss", 4, "supersampling factor")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("mkdir: %v", err)
	}

	poly := fitUnit(bolt24, *pad)
	alphas := make(map[int][][]uint8, len(sizes))
	for _, n := range sizes {
		alphas[n] = rasterAlpha(poly, n, *ss)
	}

	activePath := filepath.Join(*outDir, "tray-active.ico")
	stoppedPath := filepath.Join(*outDir, "tray-stopped.ico")
	if err := os.WriteFile(activePath, buildICO(sizes, alphas, activeRGB), 0o600); err != nil {
		fatal("write active: %v", err)
	}
	if err := os.WriteFile(stoppedPath, buildICO(sizes, alphas, stoppedRGB), 0o600); err != nil {
		fatal("write stopped: %v", err)
	}
	fmt.Printf("written: %s (%d bytes)\n", activePath, fileSize(activePath))
	fmt.Printf("written: %s (%d bytes)\n", stoppedPath, fileSize(stoppedPath))

	// ---- self-verification ----
	ok := true
	aLayers, err := parseICO(mustRead(activePath))
	if err != nil {
		fatal("verify active: %v", err)
	}
	sLayers, err := parseICO(mustRead(stoppedPath))
	if err != nil {
		fatal("verify stopped: %v", err)
	}
	if len(aLayers) != len(sizes) || len(sLayers) != len(sizes) {
		fatal("layer count = %d/%d, want %d", len(aLayers), len(sLayers), len(sizes))
	}
	for i, want := range sizes {
		a, s := aLayers[i], sLayers[i]
		if a.n != want || s.n != want {
			fmt.Printf("FAIL layer %d: sizes %d/%d, want %d\n", i, a.n, s.n, want)
			ok = false
		}
		if a.rgb != activeRGB || s.rgb != stoppedRGB {
			fmt.Printf("FAIL layer %d: colors %v/%v\n", i, a.rgb, s.rgb)
			ok = false
		}
		solid := 0
		pixelDiff := 0
		for y := 0; y < want; y++ {
			for x := 0; x < want; x++ {
				if a.alpha[y][x] != s.alpha[y][x] {
					pixelDiff++
				}
				if a.alpha[y][x] == 255 {
					solid++
				}
			}
		}
		fmt.Printf("layer %2dpx: solid=%d/%d (%.0f%%), alpha-diff vs stopped=%d, color=%02x%02x%02x\n",
			want, solid, want*want, float64(solid)/float64(want*want)*100, pixelDiff,
			a.rgb[0], a.rgb[1], a.rgb[2])
		if pixelDiff != 0 {
			fmt.Printf("FAIL layer %d: silhouettes differ in %d pixels\n", i, pixelDiff)
			ok = false
		}
		if solid == 0 {
			fmt.Printf("FAIL layer %d: empty\n", i)
			ok = false
		}
	}

	preview := filepath.Join(*outDir, "preview-tray-icons.png")
	if err := renderPreview(preview, alphas); err != nil {
		fatal("preview: %v", err)
	}
	fmt.Printf("preview: %s\n", preview)

	if !ok {
		os.Exit(1)
	}
	fmt.Println("VERIFY: PASS")
}

func fileSize(p string) int {
	fi, err := os.Stat(p)
	if err != nil {
		return -1
	}
	return int(fi.Size())
}

func mustRead(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		fatal("read %s: %v", p, err)
	}
	return b
}

func fatal(f string, a ...any) {
	fmt.Printf("FATAL: "+f+"\n", a...)
	os.Exit(1)
}

// renderPreview draws light/dark taskbar simulations plus an 8x zoom of the
// 16px layer so a human can judge legibility.
func renderPreview(path string, alphas map[int][][]uint8) error {
	const W, H = 660, 400
	var (
		light = color.RGBA{0xF3, 0xF3, 0xF3, 0xFF}
		dark  = color.RGBA{0x20, 0x20, 0x20, 0xFF}
	)
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	fillRect(img, image.Rect(0, 0, W, H), color.RGBA{0xFF, 0xFF, 0xFF, 0xFF})

	// Panel 1: light taskbar simulation.
	fillRect(img, image.Rect(20, 20, 640, 140), light)
	blitRow(img, 30, 40, alphas, activeRGB)
	blitRow(img, 350, 40, alphas, stoppedRGB)

	// Panel 2: dark taskbar simulation.
	fillRect(img, image.Rect(20, 160, 640, 280), dark)
	blitRow(img, 30, 180, alphas, activeRGB)
	blitRow(img, 350, 180, alphas, stoppedRGB)

	// Panel 3: 8x zoom of the 16px layers on white.
	zoom(img, 60, 300, alphas[16], activeRGB, 8)
	zoom(img, 400, 300, alphas[16], stoppedRGB, 8)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// blitRow draws 16/24/32 variants side by side, alpha-blended onto the panel.
func blitRow(img *image.RGBA, ox, oy int, alphas map[int][][]uint8, rgb [3]uint8) {
	x := ox
	for _, n := range sizes {
		blit(img, x, oy+32-n, n, alphas[n], rgb) // bottom-align like a real tray
		x += n + 14
	}
}

func blit(img *image.RGBA, ox, oy, n int, alpha [][]uint8, rgb [3]uint8) {
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			a := float64(alpha[y][x]) / 255
			if a <= 0 {
				continue
			}
			bg := img.RGBAAt(ox+x, oy+y)
			r := uint8(math.Round(a*float64(rgb[0]) + (1-a)*float64(bg.R)))
			g := uint8(math.Round(a*float64(rgb[1]) + (1-a)*float64(bg.G)))
			b := uint8(math.Round(a*float64(rgb[2]) + (1-a)*float64(bg.B)))
			img.SetRGBA(ox+x, oy+y, color.RGBA{r, g, b, 0xFF})
		}
	}
}

// zoom draws the 16px grid magnified by k with visible pixel structure.
func zoom(img *image.RGBA, ox, oy int, alpha [][]uint8, rgb [3]uint8, k int) {
	n := len(alpha)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			a := float64(alpha[y][x]) / 255
			c := color.RGBA{
				uint8(math.Round(a*float64(rgb[0]) + (1-a)*255)),
				uint8(math.Round(a*float64(rgb[1]) + (1-a)*255)),
				uint8(math.Round(a*float64(rgb[2]) + (1-a)*255)),
				0xFF,
			}
			fillRect(img, image.Rect(ox+x*k, oy+y*k, ox+(x+1)*k, oy+(y+1)*k), c)
		}
	}
}

func fillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}
