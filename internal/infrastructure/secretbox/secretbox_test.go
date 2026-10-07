package secretbox

import (
	"encoding/base64"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	b, err := New("key-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"", "JBSWY3DPEHPK3PXP", "ünïcode ✓"} {
		sealed, err := b.Seal(plain)
		if err != nil {
			t.Fatal(err)
		}
		if sealed == plain && plain != "" {
			t.Errorf("Seal(%q) left plaintext", plain)
		}
		got, err := b.Open(sealed)
		if err != nil || got != plain {
			t.Errorf("Open(Seal(%q)) = %q, %v", plain, got, err)
		}
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	b, _ := New("key-1")
	x, _ := b.Seal("same")
	y, _ := b.Seal("same")
	if x == y {
		t.Error("two seals of the same secret are identical (nonce reuse)")
	}
}

func TestEmptyKey(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestOpenRejects(t *testing.T) {
	b, _ := New("key-1")
	other, _ := New("key-2")
	sealed, _ := b.Seal("secret")
	raw, _ := base64.RawStdEncoding.DecodeString(sealed)
	raw[len(raw)-1] ^= 1
	tampered := base64.RawStdEncoding.EncodeToString(raw)

	for name, in := range map[string]string{
		"not base64": "%%%",
		"too short":  base64.RawStdEncoding.EncodeToString([]byte("abc")),
		"tampered":   tampered,
	} {
		if _, err := b.Open(in); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if _, err := other.Open(sealed); err == nil {
		t.Error("opened with the wrong key")
	}
}
