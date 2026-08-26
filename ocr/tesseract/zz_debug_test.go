package tesseract

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	"image/png"
	"os"
	"testing"

	"github.com/danlock/gogosseract"
)

func TestZZDebug(t *testing.T) {
	data := trainingData(t)
	ctx := context.Background()

	raw := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	reencode := func(b []byte, conv func(image.Image) image.Image) []byte {
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if conv != nil {
			img = conv(img)
		}
		var out bytes.Buffer
		if err := png.Encode(&out, img); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	toRGBA := func(src image.Image) image.Image {
		dst := image.NewRGBA(src.Bounds())
		draw.Draw(dst, src.Bounds(), src, src.Bounds().Min, draw.Src)
		return dst
	}

	docs := raw("/home/jb/go/pkg/mod/github.com/danlock/gogosseract@v0.0.11-0ad3421/internal/wasm/testdata/docs.png")
	corpus := raw("../../eval/corpus/invoices/003.png")

	cases := []struct {
		name string
		img  []byte
	}{
		{"docs.png verbatim", docs},
		{"docs.png reencoded", reencode(docs, nil)},
		{"corpus verbatim", corpus},
		{"corpus reencoded gray", reencode(corpus, nil)},
		{"corpus reencoded rgba", reencode(corpus, toRGBA)},
	}

	for _, tc := range cases {
		tess, err := gogosseract.New(ctx, gogosseract.Config{Language: "eng", TrainingData: bytes.NewReader(data)})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		err = tess.LoadImage(ctx, bytes.NewReader(tc.img), gogosseract.LoadImageOptions{})
		if err != nil {
			t.Logf("%-24s LoadImage FAIL: %v", tc.name, firstLine(err.Error()))
			_ = tess.Close(ctx)
			continue
		}
		h, err := tess.GetHOCR(ctx, nil)
		if err != nil {
			t.Logf("%-24s GetHOCR FAIL: %v", tc.name, firstLine(err.Error()))
		} else {
			t.Logf("%-24s OK, hocr %d bytes", tc.name, len(h))
		}
		_ = tess.Close(ctx)
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}
