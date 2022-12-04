package pmatch

// #cgo CFLAGS: -Wall -Wextra -pedantic -std=c99
// #cgo CFLAGS: -O2
// #cgo amd64 CFLAGS: -march=skylake -mtune=skylake
// #cgo arm CFLAGS: -mcpu=cortex-a53 -mfpu=neon-vfpv4 -mtune=cortex-a53
// #cgo arm64 CFLAGS: -march=armv8-a+crc -mcpu=cortex-a72 -mtune=cortex-a72
// #cgo LDFLAGS: -lm
// #include "c.h"
import "C"
import "image"

func SearchGrayC(img, pat *image.Gray) (int, int, float64) {
	if pat.Bounds().Size().X > img.Bounds().Size().X ||
		pat.Bounds().Size().Y > img.Bounds().Size().Y {
		panic("patch too large")
	}

	// search rect in img coordinates
	searchRect := image.Rectangle{
		Min: img.Bounds().Min,
		Max: img.Bounds().Max.Sub(pat.Rect.Size()),
	}

	m, n := searchRect.Dx(), searchRect.Dy()
	du, dv := pat.Rect.Dx(), pat.Rect.Dy()

	is := img.Stride
	ps := pat.Stride

	imgX0, imgY0 := img.Rect.Min.X, img.Rect.Min.Y
	patX0, patY0 := pat.Rect.Min.X, pat.Rect.Min.Y

	var maxX, maxY C.int
	var maxScore C.float64

	C.SearchGrayC(
		C.int(m), C.int(n), C.int(du), C.int(dv), C.int(is), C.int(ps), C.int(imgX0),
		C.int(imgY0), C.int(patX0), C.int(patY0),
		(*C.uint8_t)(&img.Pix[0]),
		(*C.uint8_t)(&pat.Pix[0]),
		(*C.int)(&maxX),
		(*C.int)(&maxY),
		(*C.float64)(&maxScore),
	)

	return int(maxX), int(maxY), float64(maxScore)
}
