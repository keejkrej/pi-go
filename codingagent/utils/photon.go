// Ported from packages/coding-agent/src/utils/photon.ts (pi v1.5.2).

package utils

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var photonLoadErr error

// PhotonImage wraps an in-memory NRGBA image with resize, orientation, and encode operations
// using pure Go packages (disintegration/imaging + golang.org/x/image/*).
type PhotonImage struct {
	img *image.NRGBA
}

// LoadPhoton verifies that the pure-Go imaging pipeline is available.
func LoadPhoton() error {
	return photonLoadErr
}

// PhotonImageFromBytes decodes PNG, JPEG, GIF, WebP, or BMP bytes into a PhotonImage.
func PhotonImageFromBytes(data []byte) (*PhotonImage, error) {
	if photonLoadErr != nil {
		return nil, photonLoadErr
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return &PhotonImage{img: imaging.Clone(decoded)}, nil
}

// NewPhotonImage constructs a PhotonImage from raw tightly-packed RGBA pixels (4 bytes/pixel).
func NewPhotonImage(rgba []byte, width, height int) (*PhotonImage, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("image dimensions must be positive")
	}
	expected := width * height * 4
	if len(rgba) < expected {
		return nil, errors.New("insufficient pixel data")
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, width, height))
	copy(nrgba.Pix, rgba[:expected])
	return &PhotonImage{img: nrgba}, nil
}

// Width returns the pixel width of the image.
func (p *PhotonImage) Width() int {
	if p == nil || p.img == nil {
		return 0
	}
	return p.img.Bounds().Dx()
}

// Height returns the pixel height of the image.
func (p *PhotonImage) Height() int {
	if p == nil || p.img == nil {
		return 0
	}
	return p.img.Bounds().Dy()
}

// Resize returns a resized copy of the image using Lanczos resampling.
func (p *PhotonImage) Resize(width, height int) *PhotonImage {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return &PhotonImage{img: imaging.Resize(p.img, width, height, imaging.Lanczos)}
}

// FlipH returns a horizontally mirrored copy of the image.
func (p *PhotonImage) FlipH() *PhotonImage {
	return &PhotonImage{img: imaging.FlipH(p.img)}
}

// FlipV returns a vertically mirrored copy of the image.
func (p *PhotonImage) FlipV() *PhotonImage {
	return &PhotonImage{img: imaging.FlipV(p.img)}
}

// Rotate90CW returns a copy rotated 90 degrees clockwise.
func (p *PhotonImage) Rotate90CW() *PhotonImage {
	// disintegration/imaging rotates counter-clockwise: Rotate270 == 90° clockwise.
	return &PhotonImage{img: imaging.Rotate270(p.img)}
}

// Rotate180 returns a copy rotated 180 degrees.
func (p *PhotonImage) Rotate180() *PhotonImage {
	return &PhotonImage{img: imaging.Rotate180(p.img)}
}

// Rotate270CW returns a copy rotated 270 degrees clockwise (90 degrees counter-clockwise).
func (p *PhotonImage) Rotate270CW() *PhotonImage {
	return &PhotonImage{img: imaging.Rotate90(p.img)}
}

// GetBytes encodes the image as PNG bytes.
func (p *PhotonImage) GetBytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, p.img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GetBytesJpeg encodes the image as JPEG bytes with the given quality (1..100).
func (p *PhotonImage) GetBytesJpeg(quality int) ([]byte, error) {
	if quality < 1 {
		quality = 1
	} else if quality > 100 {
		quality = 100
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, p.img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Free is retained for API parity with the WASM Photon binding; Go relies on garbage collection.
func (p *PhotonImage) Free() {}
