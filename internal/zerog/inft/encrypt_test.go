package inft

import (
	"crypto/rand"
	"testing"
)

func TestEncryptMetadata_Roundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	meta := map[string]string{
		"model":    "qwen-2.5-7b",
		"job_id":   "job-123",
		"result":   "inference output data",
		"duration": "1.5s",
	}

	encrypted, err := encryptMetadata(key, "key-1", meta)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	if encrypted.Algorithm != "AES-256-GCM" {
		t.Errorf("expected AES-256-GCM, got %s", encrypted.Algorithm)
	}
	if encrypted.KeyID != "key-1" {
		t.Errorf("expected key-1, got %s", encrypted.KeyID)
	}
	if len(encrypted.Ciphertext) == 0 {
		t.Error("ciphertext is empty")
	}
	if len(encrypted.Nonce) == 0 {
		t.Error("nonce is empty")
	}

	decrypted, err := decryptMetadata(key, encrypted)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	for k, v := range meta {
		if decrypted[k] != v {
			t.Errorf("key %q: expected %q, got %q", k, v, decrypted[k])
		}
	}
}

func TestEncryptMetadata_EmptyMap(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	encrypted, err := encryptMetadata(key, "key-1", map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	decrypted, err := decryptMetadata(key, encrypted)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if len(decrypted) != 0 {
		t.Errorf("expected empty map, got %v", decrypted)
	}
}

func TestEncryptMetadata_InvalidKeySize(t *testing.T) {
	tests := []struct {
		name    string
		keySize int
	}{
		{"too short", 16},
		{"too long", 64},
		{"empty", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := make([]byte, tt.keySize)
			_, err := encryptMetadata(key, "key-1", map[string]string{"k": "v"})
			if err == nil {
				t.Error("expected error for invalid key size")
			}
		})
	}
}

func TestDecryptMetadata_WrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	encrypted, err := encryptMetadata(key1, "key-1", map[string]string{"secret": "data"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = decryptMetadata(key2, encrypted)
	if err == nil {
		t.Error("expected error when decrypting with wrong key")
	}
}
