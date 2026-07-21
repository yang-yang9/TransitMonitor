package secrets

import "testing"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := DeriveKey("correct horse battery staple")
	ct, nonce, err := Encrypt(key, []byte("sk-abc123"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if string(ct) == "sk-abc123" {
		t.Error("ciphertext must not equal plaintext")
	}
	pt, err := Decrypt(key, ct, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(pt) != "sk-abc123" {
		t.Errorf("round-trip: want sk-abc123 got %s", pt)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	ct, nonce, _ := Encrypt(DeriveKey("key-a"), []byte("secret"))
	if _, err := Decrypt(DeriveKey("key-b"), ct, nonce); err == nil {
		t.Error("decrypt with wrong key must fail")
	}
}

func TestRedact(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sk-abc123", "sk-***"},
		{"pat-xyz", "pat***"},
		{"ab", "***"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Redact(c.in); got != c.want {
			t.Errorf("Redact(%q): want %q got %q", c.in, c.want, got)
		}
	}
}
