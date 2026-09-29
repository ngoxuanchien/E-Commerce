// Command genimages writes the deterministic product images in
// e2e/fixtures/images (run from e2e/: go run ./cmd/genimages).
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
)

type family struct {
	name  string
	count int
	base  color.RGBA
}

var families = []family{
	{"fashion", 7, color.RGBA{R: 214, G: 69, B: 65, A: 255}},
	{"electronics", 8, color.RGBA{R: 38, G: 99, B: 191, A: 255}},
	{"home", 7, color.RGBA{R: 46, G: 139, B: 87, A: 255}},
}

func main() {
	dir := filepath.Join("fixtures", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, f := range families {
		for i := 1; i <= f.count; i++ {
			path := filepath.Join(dir, fmt.Sprintf("%s-%02d.jpg", f.name, i))
			if err := write(path, f.base, i); err != nil {
				log.Fatal(err)
			}
		}
	}
}

// write draws a diagonal gradient with a disc whose position depends on i,
// so every image in a family is visibly different.
func write(path string, base color.RGBA, i int) error {
	const size = 480
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy, r := 120+(i*37)%240, 140+(i*53)%200, 70+(i*11)%40
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			t := float64(x+y) / (2 * size)
			c := color.RGBA{
				R: uint8(float64(base.R)*(1-t) + 250*t),
				G: uint8(float64(base.G)*(1-t) + 245*t),
				B: uint8(float64(base.B)*(1-t) + 235*t),
				A: 255,
			}
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy < r*r {
				c = color.RGBA{R: 255, G: 255, B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
