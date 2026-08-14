package media

import (
	"crypto/sha1" //nolint:gosec // Cloudinary's signature scheme mandates SHA-1; not our choice.
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// UploadSignatureResponse is what the browser needs to perform a SIGNED
// upload directly to Cloudinary.
//
// Note what is NOT here: the API secret. The secret never leaves the
// server. The browser receives only a short-lived signature computed from
// it, which authorises exactly one upload with exactly these parameters.
type UploadSignatureResponse struct {
	Signature string `json:"signature"`
	Timestamp int64  `json:"timestamp"`
	APIKey    string `json:"api_key"`
	CloudName string `json:"cloud_name"`
	Folder    string `json:"folder"`
}

// uploadFolder scopes every upload to a single folder. Included in the
// signed parameters, so a caller cannot redirect uploads elsewhere in the
// account by editing the request - changing it invalidates the signature.
const uploadFolder = "bedge-media"

// GenerateUploadSignature builds a Cloudinary upload signature.
//
// Why this endpoint exists: uploads previously used an UNSIGNED preset,
// meaning the cloud name and preset (both visible in the shipped JS bundle)
// were the only things needed to upload. Anyone who opened devtools could
// push unlimited content into the account - a billing and content-moderation
// exposure with no authentication whatsoever.
//
// With signed uploads, Cloudinary rejects anything lacking a valid
// signature, and only an authenticated artist can obtain one.
//
// The algorithm is Cloudinary's, not ours: take every parameter that will
// be sent (excluding file, api_key, resource_type and the signature
// itself), sort by key, join as k=v with &, append the API secret, then
// SHA-1 the result. SHA-1 is weak generally but is what Cloudinary's API
// specifies here, and it is used as a shared-secret MAC rather than for
// collision resistance.
func GenerateUploadSignature() (*UploadSignatureResponse, error) {
	apiKey := os.Getenv("CLOUDINARY_API_KEY")
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")
	cloudName := os.Getenv("CLOUDINARY_CLOUD_NAME")

	// Defensive: ValidateEnv already fails fast at boot if these are
	// missing, so reaching here means something changed at runtime.
	// Returning an error is better than signing with an empty secret,
	// which would produce a valid-looking but useless signature.
	if apiKey == "" || apiSecret == "" || cloudName == "" {
		return nil, fmt.Errorf("cloudinary credentials are not configured")
	}

	timestamp := time.Now().Unix()

	// Only the params Cloudinary includes in the signature, sorted by key.
	params := map[string]string{
		"folder":    uploadFolder,
		"timestamp": fmt.Sprintf("%d", timestamp),
	}

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
	return &UploadSignatureResponse{
		Signature: hex.EncodeToString(sum[:]),
		Timestamp: timestamp,
		APIKey:    apiKey,
		CloudName: cloudName,
		Folder:    uploadFolder,
	}, nil
}
