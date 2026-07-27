package auth

import (
	"fmt"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const totpIssuer = "Congopro Bridge"

// GenerateTOTPSecret creates a new TOTP secret and its otpauth:// provisioning
// URI for accountEmail — print the URI (or a QR code rendered from it) so the
// user can enroll it in an authenticator app; there's no other channel to
// deliver it through since staff auth doesn't depend on email.
func GenerateTOTPSecret(accountEmail string) (secret, uri string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: accountEmail,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// VerifyTOTPCode checks a 6-digit code against the stored secret, allowing
// the standard ±1 time-step (±30s) skew for clock drift between server and phone.
func VerifyTOTPCode(secret, code string) bool {
	valid, err := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	return err == nil && valid
}
