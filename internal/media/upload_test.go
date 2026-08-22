package media

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // matches the implementation under test
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validPNG builds a real, tiny, valid PNG for tests that need genuine
// image bytes - not a stub, since the whole point of validateAndReencode
// is to actually decode the input.
func validPNG(t *testing.T, w, h int, withAlpha bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			if withAlpha {
				img.Set(x, y, color.RGBA{R: 200, G: 50, B: 50, A: 128})
			} else {
				img.Set(x, y, color.RGBA{R: 200, G: 50, B: 50, A: 255})
			}
		}
	}
	buf := &bytes.Buffer{}
	require.NoError(t, png.Encode(buf, img))
	return buf.Bytes()
}

// TestValidateAndReencode_RejectsDisguisedNonImage is the test that
// matters most: this is the actual malware/exploit defense. A text file
// renamed to look like an upload (or any other non-image content) must
// never make it past this check, regardless of what content-type or
// filename the client claims.
func TestValidateAndReencode_RejectsDisguisedNonImage(t *testing.T) {
	fakeImage := []byte("#!/bin/sh\necho 'this is not an image, no matter what you name it'\n")

	_, _, err := validateAndReencode(fakeImage)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotAnImage)
}

// TestValidateAndReencode_RejectsTruncatedImage covers the "corrupt/
// malformed" half of ErrNotAnImage - bytes that sniff as an image
// (correct magic number) but fail to actually decode. A real PNG chopped
// off mid-stream is exactly what a lot of malformed/exploit-attempt
// uploads look like at the byte level.
func TestValidateAndReencode_RejectsTruncatedImage(t *testing.T) {
	full := validPNG(t, 50, 50, false)
	truncated := full[:len(full)/2]

	_, _, err := validateAndReencode(truncated)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotAnImage)
}

// TestValidateAndReencode_AcceptsRealImage_ReencodesAsJPEG is the happy
// path: a real opaque PNG comes in, and what goes out is bytes we
// produced ourselves (verified by decoding the result and comparing
// dimensions), not the original file untouched.
func TestValidateAndReencode_AcceptsRealImage_ReencodesAsJPEG(t *testing.T) {
	raw := validPNG(t, 64, 48, false)

	out, ext, err := validateAndReencode(raw)

	require.NoError(t, err)
	assert.Equal(t, "jpg", ext, "an opaque source image should re-encode as JPEG, not carry its original format through")

	decoded, err := jpeg.Decode(bytes.NewReader(out.Bytes()))
	require.NoError(t, err, "the re-encoded output must itself be a valid, decodable image")
	assert.Equal(t, 64, decoded.Bounds().Dx())
	assert.Equal(t, 48, decoded.Bounds().Dy())
}

// TestValidateAndReencode_PreservesTransparencyAsPNG ensures a genuinely
// translucent source doesn't silently lose its alpha channel - JPEG has
// no transparency support, so flattening a logo/screenshot with real
// alpha to JPEG would visibly break it.
func TestValidateAndReencode_PreservesTransparencyAsPNG(t *testing.T) {
	raw := validPNG(t, 40, 40, true)

	out, ext, err := validateAndReencode(raw)

	require.NoError(t, err)
	assert.Equal(t, "png", ext)

	decoded, err := png.Decode(bytes.NewReader(out.Bytes()))
	require.NoError(t, err)
	_, _, _, a := decoded.At(0, 0).RGBA()
	assert.NotEqual(t, uint32(0xffff), a, "the re-encoded PNG should still carry real transparency")
}

// TestValidateAndReencode_RejectsDecompressionBomb guards against a small
// file that claims an enormous pixel grid - width*height can be attacker-
// controlled independent of byte size, and encoding a truly huge image
// would be an easy way to exhaust server memory/CPU with a tiny upload.
func TestValidateAndReencode_RejectsDecompressionBomb(t *testing.T) {
	// 10001 x 10001 exceeds maxDecodedPixels (40,000,000) while still
	// being a real, validly-decodable image - this isn't testing decode
	// failure, it's testing the pixel-count guard specifically.
	raw := validPNG(t, 10001, 10001, false)

	_, _, err := validateAndReencode(raw)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrImageTooManyPixels)
}

// TestSignUploadParams_MatchesCloudinaryAlgorithm recomputes the signature
// independently using Cloudinary's documented scheme (sorted k=v joined by
// &, secret appended, SHA-1) and asserts our implementation matches. A
// test that only checked "a non-empty string came back" would pass against
// a completely wrong algorithm, and the failure would only surface as
// every upload being rejected by Cloudinary in production.
func TestSignUploadParams_MatchesCloudinaryAlgorithm(t *testing.T) {
	got := signUploadParams(map[string]string{
		"folder":    "bedge-media",
		"timestamp": "1700000000",
	}, "test-secret")

	// folder < timestamp alphabetically, so this order is what the
	// algorithm must produce.
	expectedInput := "folder=bedge-media&timestamp=1700000000test-secret"
	sum := sha1.Sum([]byte(expectedInput)) //nolint:gosec
	assert.Equal(t, hex.EncodeToString(sum[:]), got)
}

// TestParseCloudinaryResponse_MissingSecureURL_Errors ensures a malformed
// or unexpected Cloudinary response is treated as a failure rather than
// silently returning an empty URL that would later get stored as a
// photo's URL.
func TestParseCloudinaryResponse_MissingSecureURL_Errors(t *testing.T) {
	_, err := parseCloudinaryResponse([]byte(`{"public_id": "bedge-media/abc123"}`))

	require.Error(t, err)
}

func TestParseCloudinaryResponse_Success(t *testing.T) {
	result, err := parseCloudinaryResponse([]byte(`{"secure_url": "https://res.cloudinary.com/x/image/upload/v1/bedge-media/abc123.jpg", "public_id": "bedge-media/abc123"}`))

	require.NoError(t, err)
	assert.Equal(t, "https://res.cloudinary.com/x/image/upload/v1/bedge-media/abc123.jpg", result.URL)
	assert.Equal(t, "bedge-media/abc123", result.CloudinaryID)
}
