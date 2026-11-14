package operator

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeyLength is the length of PMS_OPERATOR_TOTP_KEY in bytes: AES-256 (decision P5). config.ParseOperatorKey reads it.
const KeyLength = 32

// additionalData ties a ciphertext to what it is: a secret copied to another column does not open.
var additionalData = []byte("kamarapms operator totp secret v1")

// seal encrypts plain with AES-256-GCM: the result is nonce || ciphertext || tag.
func seal(key, plain []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("operator: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plain, additionalData), nil
}

// open decrypts what seal made; a wrong key or a changed byte is an error.
func open(key, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("operator: the sealed secret is too short")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], additionalData)
	if err != nil {
		return nil, errors.New("operator: the sealed secret does not open with this key")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != KeyLength {
		return nil, fmt.Errorf("operator: the key must be %d bytes", KeyLength)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
