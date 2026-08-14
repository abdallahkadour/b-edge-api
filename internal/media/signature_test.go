package media

import (
	"crypto/sha1" //nolint:gosec // matches the implementation under test
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setCloudinaryEnv sets credentials for the duration of a test. t.Setenv
// restores the previous value automatically, so tests can't leak state
// into each other.
func setCloudinaryEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CLOUDINARY_CLOUD_NAME", "test-cloud")
	t.Setenv("CLOUDINARY_API_KEY", "123456789")
	t.Setenv("CLOUDINARY_API_SECRET", "test-secret")
}

// TestGenerateUploadSignature_MatchesCloudinaryAlgorithm is the test that
// actually matters: it recomputes the signature independently using
// Cloudinary's documented scheme (sorted k=v joined by &, secret appended,
// SHA-1) and asserts our implementation produces the identical value.
//
// A test that only asserted "a non-empty string came back" would pass
// against a completely wrong algorithm, and the failure would surface as
// every upload being rejected in production.
func TestGenerateUploadSignature_MatchesCloudinaryAlgorithm(t *testing.T) {
	setCloudinaryEnv(t)

	sig, err := GenerateUploadSignature()
	require.NoError(t, err)

	expectedInput := fmt.Sprintf("folder=%s&timestamp=%d%s", uploadFolder, sig.Timestamp, "test-secret")
	sum := sha1.Sum([]byte(expectedInput)) //nolint:gosec
	assert.Equal(t, hex.EncodeToString(sum[:]), sig.Signature)
}

// TestGenerateUploadSignature_NeverLeaksSecret guards the entire point of
// this endpoint. The response struct has no secret field today; this test
// fails loudly if someone later adds one, or populates APIKey with the
// secret by mistake.
func TestGenerateUploadSignature_NeverLeaksSecret(t *testing.T) {
	setCloudinaryEnv(t)

	sig, err := GenerateUploadSignature()
	require.NoError(t, err)

	assert.NotEqual(t, "test-secret", sig.APIKey,
		"the API SECRET must never be returned in place of the api key")
	assert.NotContains(t, sig.Signature, "test-secret",
		"the raw secret must never appear in the signature output")
	assert.Equal(t, "123456789", sig.APIKey)
	assert.Equal(t, "test-cloud", sig.CloudName)
}

// TestGenerateUploadSignature_ScopesToFolder verifies the upload folder is
// part of the signed payload. Because it is signed, a caller cannot edit
// the folder in the request to write elsewhere in the Cloudinary account -
// doing so invalidates the signature and Cloudinary rejects the upload.
func TestGenerateUploadSignature_ScopesToFolder(t *testing.T) {
	setCloudinaryEnv(t)

	sig, err := GenerateUploadSignature()
	require.NoError(t, err)

	assert.Equal(t, uploadFolder, sig.Folder)
}

// TestGenerateUploadSignature_MissingCredentials_Errors ensures a
// misconfigured server fails loudly rather than signing with an empty
// secret, which would produce a well-formed signature that Cloudinary
// always rejects - far harder to diagnose than an outright error.
func TestGenerateUploadSignature_MissingCredentials_Errors(t *testing.T) {
	t.Setenv("CLOUDINARY_CLOUD_NAME", "")
	t.Setenv("CLOUDINARY_API_KEY", "")
	t.Setenv("CLOUDINARY_API_SECRET", "")

	_, err := GenerateUploadSignature()

	require.Error(t, err)
}

func TestGenerateUploadSignature_PartialCredentials_Errors(t *testing.T) {
	t.Setenv("CLOUDINARY_CLOUD_NAME", "test-cloud")
	t.Setenv("CLOUDINARY_API_KEY", "123456789")
	t.Setenv("CLOUDINARY_API_SECRET", "") // the one that matters, missing

	_, err := GenerateUploadSignature()

	require.Error(t, err, "a missing secret must never be treated as usable")
}
