package sharelink

import (
	"errors"
	"testing"
	"time"
)

func testSigner() Signer {
	return Signer{Current: Key{ID: "k1", Secret: []byte("secret-1")}}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	s := testSigner()
	token, err := s.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sid, cid, err := s.Verify(token, time.Now())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sid != "sl_1" || cid != "cap_1" {
		t.Fatalf("got sid=%q cid=%q", sid, cid)
	}
}

func TestVerify_Expired(t *testing.T) {
	s := testSigner()
	token, _ := s.Sign("sl_1", "cap_1", time.Now().Add(-time.Second))
	_, _, err := s.Verify(token, time.Now())
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestVerify_MalformedNoSeparator(t *testing.T) {
	s := testSigner()
	_, _, err := s.Verify("no-dot-here", time.Now())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerify_BadBase64Payload(t *testing.T) {
	s := testSigner()
	_, _, err := s.Verify("not-valid-base64!!!.alsoinvalid!!!", time.Now())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerify_BadBase64Signature(t *testing.T) {
	s := testSigner()
	_, _, err := s.Verify(encode([]byte(`{}`))+".not-valid-base64!!!", time.Now())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerify_MalformedJSON(t *testing.T) {
	s := testSigner()
	raw := []byte("not json")
	sig := sign(s.Current.Secret, raw)
	token := encode(raw) + "." + encode(sig)
	_, _, err := s.Verify(token, time.Now())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerify_UnknownKeyID(t *testing.T) {
	signed := Signer{Current: Key{ID: "unknown-key", Secret: []byte("x")}}
	token, _ := signed.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	verifier := testSigner() // only knows "k1"
	_, _, err := verifier.Verify(token, time.Now())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestVerify_WrongSecretSameKeyID(t *testing.T) {
	signer := Signer{Current: Key{ID: "k1", Secret: []byte("secret-a")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	verifier := Signer{Current: Key{ID: "k1", Secret: []byte("secret-b")}}
	_, _, err := verifier.Verify(token, time.Now())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

// TestVerify_RotatedKey exercises key rotation: a token signed under the
// previous current key must still verify once that key moves to Previous.
func TestVerify_RotatedKey(t *testing.T) {
	oldKey := Key{ID: "k1", Secret: []byte("old-secret")}
	oldSigner := Signer{Current: oldKey}
	token, _ := oldSigner.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	rotated := Signer{
		Current:  Key{ID: "k2", Secret: []byte("new-secret")},
		Previous: []Key{oldKey},
	}
	sid, cid, err := rotated.Verify(token, time.Now())
	if err != nil {
		t.Fatalf("verify after rotation: %v", err)
	}
	if sid != "sl_1" || cid != "cap_1" {
		t.Fatalf("got sid=%q cid=%q", sid, cid)
	}
}

func TestSplitOnce_NoSeparator(t *testing.T) {
	_, _, ok := splitOnce("nosep", '.')
	if ok {
		t.Fatalf("expected ok=false")
	}
}
