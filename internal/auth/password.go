package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// bcrypt silently truncates input past 72 bytes — reject anything longer
// instead of accepting a password that's actually shorter than it looks.
const maxPasswordBytes = 72

const MinPasswordLength = 12

var ErrPasswordTooLong = errors.New("password exceeds 72 bytes")
var ErrPasswordTooShort = errors.New("password must be at least 12 characters")

func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	if len(password) > maxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
