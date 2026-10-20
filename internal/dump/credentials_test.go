package dump

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func mintToken(t *testing.T, sub string, exp int64) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"iss": "steam", "sub": sub, "aud": []string{"client"}, "exp": exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := base64.RawURLEncoding.EncodeToString(claims)
	return fmt.Sprintf("header.%s.signature", body)
}

const testSteamID = "76561190000000000"

type fakeStore struct {
	saved map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{saved: map[string]string{}}
}

func (f *fakeStore) Save(account, token string) error {
	f.saved["token/"+account] = token
	f.saved["last-account"] = account
	return nil
}

func (f *fakeStore) Load() (account, token string, ok bool, err error) {
	account, ok = f.saved["last-account"]
	if !ok {
		return "", "", false, nil
	}
	token, ok = f.saved["token/"+account]
	if !ok {
		return "", "", false, nil
	}
	return account, token, true, nil
}

func (f *fakeStore) Forget() error {
	f.saved = map[string]string{}
	return nil
}

func TestTokenStoreRoundTrip(t *testing.T) {
	s := newFakeStore()
	if _, _, ok, _ := s.Load(); ok {
		t.Fatal("expected empty store")
	}
	if err := s.Save("alice", "tok"); err != nil {
		t.Fatal(err)
	}
	account, token, ok, err := s.Load()
	if err != nil || !ok || account != "alice" || token != "tok" {
		t.Fatalf("Load = %q %q %v %v", account, token, ok, err)
	}
	if err := s.Forget(); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.Load(); ok {
		t.Fatal("expected forget to clear store")
	}
}

func TestStoredClientRejectsBadToken(t *testing.T) {
	s := newFakeStore()
	if err := s.Save("alice", "not-a-jwt"); err != nil {
		t.Fatal(err)
	}
	client, err := storedClient(context.Background(), s, nil)
	if client != nil || err == nil {
		t.Fatalf("expected nil client with error, got %v, %v", client, err)
	}
	if !errors.Is(err, errStoredLoginUnavailable) {
		t.Fatalf("expected errStoredLoginUnavailable, got %v", err)
	}
}

func TestStoredClientDetectsExpiry(t *testing.T) {
	s := newFakeStore()
	expired := mintToken(t, testSteamID, time.Now().Add(-time.Hour).Unix())
	if err := s.Save("alice", expired); err != nil {
		t.Fatal(err)
	}
	if _, err := storedClient(context.Background(), s, nil); !errors.Is(err, errStoredLoginUnavailable) {
		t.Fatalf("expected errStoredLoginUnavailable, got %v", err)
	}
}
