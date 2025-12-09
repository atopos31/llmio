package pkg

import (
	"crypto/rand"
	"math/big"
)

const keyChars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

var keyCharsMaxI = big.NewInt(int64(len(keyChars)))

func GenerateRandomCharsKey(length int) (string, error) {
	b := make([]byte, length)

	for i := range b {
		n, err := rand.Int(rand.Reader, keyCharsMaxI)
		if err != nil {
			return "", err
		}
		b[i] = keyChars[n.Int64()]
	}

	return string(b), nil
}
