package auth

import (
	"log"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword returns a bcrypt hash of the plaintext password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("auth: password hashing failed: %v", err)
		return "", err
	}
	log.Printf("auth: password hashed successfully")
	return string(b), nil
}

// CheckPassword reports whether the plaintext password matches the hash.
func CheckPassword(hash, password string) bool {
	ok := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	if ok {
		log.Printf("auth: password verification succeeded")
	} else {
		log.Printf("auth: password verification failed")
	}
	return ok
}
