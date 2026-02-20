package inft

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
)

const encryptionAlgorithm = "AES-256-GCM"

// encryptMetadata encrypts a metadata map using AES-256-GCM.
// The key must be exactly 32 bytes for AES-256.
func encryptMetadata(key []byte, keyID string, meta map[string]string) (*EncryptedMeta, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("inft: encryption key must be 32 bytes, got %d: %w", len(key), ErrEncryptionFailed)
	}

	plaintext, err := json.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("inft: failed to serialize metadata: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("inft: failed to create cipher: %w", ErrEncryptionFailed)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("inft: failed to create GCM: %w", ErrEncryptionFailed)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("inft: failed to generate nonce: %w", ErrEncryptionFailed)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return &EncryptedMeta{
		Ciphertext: ciphertext,
		Nonce:      nonce,
		KeyID:      keyID,
		Algorithm:  encryptionAlgorithm,
	}, nil
}

// decryptMetadata decrypts AES-256-GCM encrypted metadata.
func decryptMetadata(key []byte, enc *EncryptedMeta) (map[string]string, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("inft: decryption key must be 32 bytes, got %d: %w", len(key), ErrEncryptionFailed)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("inft: failed to create cipher: %w", ErrEncryptionFailed)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("inft: failed to create GCM: %w", ErrEncryptionFailed)
	}

	plaintext, err := gcm.Open(nil, enc.Nonce, enc.Ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("inft: decryption failed: %w", ErrEncryptionFailed)
	}

	var meta map[string]string
	if err := json.Unmarshal(plaintext, &meta); err != nil {
		return nil, fmt.Errorf("inft: failed to deserialize metadata: %w", err)
	}

	return meta, nil
}
