package media

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // Cloudinary's signature scheme mandates SHA-1; not our choice.
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif" // side-effect registration only: lets image.Decode recognise GIF input
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/image/webp"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// MaxUploadBytes is the hard ceiling on any image upload - avatars,
// portfolio photos, product photos alike. 15MB comfortably covers a full-
// resolution phone-camera photo (even a modern 48MP shot is typically
// 10-15MB as a JPEG) while still bounding memory/CPU cost per upload,
// since every upload gets fully decoded into memory below.
const MaxUploadBytes = 15 * 1024 * 1024

// maxDecodedPixels caps width*height post-decode, independent of the byte-
// size check above. A small file can still decode into an enormous pixel
// grid (a "decompression bomb") - a 200KB PNG can legally claim to be
// 40000x40000 pixels, which would exhaust memory the moment we try to
// re-encode it. 40 megapixels is far beyond anything a real phone or
// camera produces (a 45MP flagship sensor is ~8400x5300 ≈ 44MP), so this
// only ever rejects deliberately hostile input.
const maxDecodedPixels = 40_000_000

// allowedContentTypes is checked against the SNIFFED content type
// (http.DetectContentType, which inspects the actual file bytes), never
// the browser-supplied File.type - that field is just a string the client
// sends and is trivially spoofed by renaming any file to end in .jpg.
var allowedContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// ErrNotAnImage means the uploaded bytes failed to sniff/decode as one of
// the allowed image formats - either a genuinely different file type
// (renamed executable, PDF, zip, ...) or a corrupt/malformed image.
var ErrNotAnImage = apperror.BadRequest("INVALID_IMAGE", "That file doesn't look like a valid image. Please choose a JPEG, PNG, GIF, or WebP.")

// ErrImageTooLarge mirrors MaxUploadBytes back to the client in a form the
// frontend's validateImageFile() helper already produces the same
// message for, so the error looks identical whether it was caught
// client-side or (if a client bypasses that check) here.
var ErrImageTooLarge = apperror.BadRequest("IMAGE_TOO_LARGE", fmt.Sprintf("That image is larger than %dMB.", MaxUploadBytes/(1024*1024)))

// ErrImageTooManyPixels is the decompression-bomb rejection - deliberately
// worded the same as a normal "too large" error rather than mentioning
// pixel counts, which would mean nothing to an actual user and would leak
// exactly how the check works to anyone probing it.
var ErrImageTooManyPixels = apperror.BadRequest("IMAGE_TOO_LARGE", "That image is too large to process. Please choose a smaller one.")

// UploadResult is what a successful upload hands back to the caller -
// the same shape the old direct-to-Cloudinary browser upload used to
// return, so nothing downstream of this (AddPhoto, AddProductPhoto, the
// artist profile's avatar_url field) needs to change.
type UploadResult struct {
	URL          string
	CloudinaryID string
}

// ProcessAndUploadImage is the full defense pipeline for one uploaded file,
// run entirely server-side:
//
//  1. Size check against MaxUploadBytes.
//  2. Sniff the REAL content type from the file's own bytes and check it
//     against allowedContentTypes - not the client-supplied filename or
//     MIME type, both of which an attacker fully controls.
//  3. Decode the image. This is the actual malware/exploit defense: a
//     disguised non-image file (an executable, script, or polyglot file
//     renamed to .jpg) will not decode as a valid image and is rejected
//     here, before it ever reaches storage.
//  4. Reject implausible pixel dimensions (decompression-bomb defense).
//  5. RE-ENCODE the decoded image into a fresh file. Only the actual pixel
//     data survives a decode/re-encode round-trip - any payload appended
//     to or hidden within the original file (EXIF-based exploits, data
//     appended past the image's real end, a polyglot's second format)
//     is discarded. What we upload to Cloudinary is always a file WE
//     produced, never the original bytes the client sent.
//  6. Upload the clean, re-encoded copy to Cloudinary using the
//     server-held API secret (never exposed to the browser).
//
// This replaces the previous flow, where the browser uploaded directly to
// Cloudinary using a signed-but-otherwise-unchecked request: Cloudinary
// received and stored the client's raw bytes untouched, and nothing
// server-side ever inspected them at all.
func ProcessAndUploadImage(ctx *multipart.FileHeader) (*UploadResult, error) {
	if ctx.Size > MaxUploadBytes {
		return nil, ErrImageTooLarge
	}

	f, err := ctx.Open()
	if err != nil {
		return nil, fmt.Errorf("process image: open upload: %w", err)
	}
	defer f.Close() //nolint:errcheck

	raw, err := io.ReadAll(io.LimitReader(f, MaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("process image: read upload: %w", err)
	}
	if len(raw) > MaxUploadBytes {
		return nil, ErrImageTooLarge
	}

	clean, ext, err := validateAndReencode(raw)
	if err != nil {
		return nil, err
	}

	return uploadToCloudinary(clean, ext)
}

// validateAndReencode is steps 2-5 of ProcessAndUploadImage's pipeline
// (sniff, decode, pixel-cap check, re-encode) split out from the
// size-check/read/upload steps around it so the actual defense logic -
// the part a test can meaningfully assert on - is exercisable directly
// against raw bytes, without needing a real multipart.FileHeader or a
// live Cloudinary account.
func validateAndReencode(raw []byte) (*bytes.Buffer, string, error) {
	sniffed := http.DetectContentType(raw)
	if !allowedContentTypes[sniffed] {
		return nil, "", ErrNotAnImage
	}

	decoded, err := decodeImage(raw, sniffed)
	if err != nil {
		return nil, "", ErrNotAnImage
	}

	bounds := decoded.Bounds()
	pixels := int64(bounds.Dx()) * int64(bounds.Dy())
	if pixels <= 0 || pixels > maxDecodedPixels {
		return nil, "", ErrImageTooManyPixels
	}

	clean, ext, err := reencodeImage(decoded)
	if err != nil {
		return nil, "", fmt.Errorf("process image: re-encode: %w", err)
	}
	return clean, ext, nil
}

// decodeImage dispatches to the right decoder for the sniffed content
// type. image/jpeg, image/png and image/gif register themselves with the
// stdlib image package via their side-effect imports, but WebP has no
// stdlib decoder, hence the explicit golang.org/x/image/webp call (pure
// Go, decode-only - there's no corresponding Go encoder, which is why
// reencodeImage below always outputs JPEG or PNG regardless of the
// source format).
func decodeImage(raw []byte, sniffedType string) (image.Image, error) {
	if sniffedType == "image/webp" {
		return webp.Decode(bytes.NewReader(raw))
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// reencodeImage produces a brand-new file from decoded pixel data only.
// PNG when the source had a real alpha channel (logos, screenshots with
// transparency - flattening these to JPEG would visibly break them),
// JPEG otherwise (the common case: real photos, where JPEG is far
// smaller). Quality 88 balances file size against visible artifacting for
// a profile/portfolio/product photo.
func reencodeImage(img image.Image) (*bytes.Buffer, string, error) {
	buf := &bytes.Buffer{}

	if hasAlpha(img) {
		if err := png.Encode(buf, img); err != nil {
			return nil, "", err
		}
		return buf, "png", nil
	}

	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, "", err
	}
	return buf, "jpg", nil
}

// hasAlpha reports whether any pixel is actually translucent - not just
// whether the color model supports an alpha channel. A JPEG decodes into
// an alpha-capable Go image.Image in some code paths, and plenty of real
// PNGs carry an alpha channel that's fully opaque everywhere; both should
// still become JPEGs. Sampling on a grid rather than every pixel keeps
// this cheap on a large photo.
func hasAlpha(img image.Image) bool {
	switch img.(type) {
	case *image.Gray, *image.Gray16, *image.CMYK, *image.YCbCr:
		return false
	}

	bounds := img.Bounds()
	const step = 7 // coprime-ish stride so a striped image isn't systematically missed
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			_, _, _, a := img.At(x, y).RGBA()
			if a < 0xffff {
				return true
			}
		}
	}
	return false
}

// cloudinaryUploadFolder scopes every upload to a single Cloudinary folder,
// matching the constant the old browser-facing signature endpoint used, so
// existing media already under "bedge-media" stays consistent with newly
// uploaded files.
const cloudinaryUploadFolder = "bedge-media"

// uploadToCloudinary performs the actual signed upload server-side using
// the same signing algorithm the removed GET /media/signature endpoint
// used to hand to the browser - the difference is this now runs entirely
// on the server, with the file bytes we produced (never the client's raw
// upload), and the signature/secret never leave the process.
func uploadToCloudinary(fileBuf *bytes.Buffer, ext string) (*UploadResult, error) {
	apiKey := os.Getenv("CLOUDINARY_API_KEY")
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")
	cloudName := os.Getenv("CLOUDINARY_CLOUD_NAME")
	if apiKey == "" || apiSecret == "" || cloudName == "" {
		return nil, fmt.Errorf("cloudinary credentials are not configured")
	}

	timestamp := time.Now().Unix()
	signature := signUploadParams(map[string]string{
		"folder":    cloudinaryUploadFolder,
		"timestamp": fmt.Sprintf("%d", timestamp),
	}, apiSecret)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)

	part, err := mw.CreateFormFile("file", "upload."+ext)
	if err != nil {
		return nil, fmt.Errorf("cloudinary upload: build form: %w", err)
	}
	if _, err := part.Write(fileBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("cloudinary upload: write file part: %w", err)
	}

	fields := map[string]string{
		"api_key":   apiKey,
		"timestamp": fmt.Sprintf("%d", timestamp),
		"signature": signature,
		"folder":    cloudinaryUploadFolder,
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("cloudinary upload: write field %s: %w", k, err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("cloudinary upload: close form: %w", err)
	}

	uploadURL := fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/image/upload", cloudName)
	req, err := http.NewRequest(http.MethodPost, uploadURL, body)
	if err != nil {
		return nil, fmt.Errorf("cloudinary upload: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudinary upload: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cloudinary upload: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cloudinary upload: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	result, err := parseCloudinaryResponse(respBody)
	if err != nil {
		return nil, fmt.Errorf("cloudinary upload: %w", err)
	}
	return result, nil
}

// signUploadParams implements Cloudinary's signing algorithm: sort every
// param (excluding file/api_key/resource_type/signature itself) by key,
// join as k=v with &, append the API secret, SHA-1 the result. Identical
// to the algorithm the old GenerateUploadSignature used - only where it
// runs has changed.
func signUploadParams(params map[string]string, apiSecret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, params[k]))
	}
	toSign := strings.Join(parts, "&") + apiSecret
	sum := sha1.Sum([]byte(toSign)) //nolint:gosec // mandated by Cloudinary's API
	return hex.EncodeToString(sum[:])
}

// cloudinaryUploadResponse is the subset of Cloudinary's upload response we
// actually need. Cloudinary returns many more fields (format, bytes,
// width, height, ...) that we have no use for.
type cloudinaryUploadResponse struct {
	SecureURL string `json:"secure_url"`
	PublicID  string `json:"public_id"`
}

func parseCloudinaryResponse(body []byte) (*UploadResult, error) {
	var parsed cloudinaryUploadResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if parsed.SecureURL == "" {
		return nil, fmt.Errorf("response missing secure_url: %s", string(body))
	}
	return &UploadResult{URL: parsed.SecureURL, CloudinaryID: parsed.PublicID}, nil
}
