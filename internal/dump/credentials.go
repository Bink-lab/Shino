package dump

import (
	"context"
	"errors"

	"github.com/zalando/go-keyring"
)

const keyringService = "Shino"

var errStoredLoginUnavailable = errors.New("no stored sign-in")

type tokenStore interface {
	Save(account, token string) error
	Load() (account, token string, ok bool, err error)
	Forget() error
}

type keyringStore struct{}

func newKeyringStore() tokenStore { return keyringStore{} }

func (keyringStore) Save(account, token string) error {
	if err := keyring.Set(keyringService, "last-account", account); err != nil {
		return err
	}
	return keyring.Set(keyringService, "token/"+account, token)
}

func (keyringStore) Load() (account, token string, ok bool, err error) {
	account, err = keyring.Get(keyringService, "last-account")
	if errors.Is(err, keyring.ErrNotFound) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	token, err = keyring.Get(keyringService, "token/"+account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return account, token, true, nil
}

func (keyringStore) Forget() error {
	account, err := keyring.Get(keyringService, "last-account")
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := keyring.Delete(keyringService, "token/"+account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return keyring.Delete(keyringService, "last-account")
}

func storedClient(ctx context.Context, store tokenStore, onStatus func(string)) (*Client, error) {
	status := func(line string) {
		if onStatus != nil {
			onStatus(line)
		}
	}
	account, token, ok, err := store.Load()
	if err != nil {
		status("stored sign-in unreadable, use qr login")
		return nil, errStoredLoginUnavailable
	}
	if !ok {
		return nil, errStoredLoginUnavailable
	}
	if _, err := checkRefreshToken(token); err != nil {
		return nil, errStoredLoginUnavailable
	}
	status("signing in with saved sign-in for " + account)
	raw, err := connectRaw(ctx)
	if err != nil {
		return nil, errStoredLoginUnavailable
	}
	client := &Client{raw: raw, http: defaultHTTPClient()}
	if logErr := logOnWithTimeout(ctx, client, account, token); logErr != nil {
		raw.Disconnect()
		return nil, errStoredLoginUnavailable
	}
	return client, nil
}

func forgetStoredSignin(store tokenStore) error {
	return store.Forget()
}

func ConnectStored(ctx context.Context, onStatus func(string)) (*Client, error) {
	return storedClient(ctx, newKeyringStore(), onStatus)
}

func NewKeyringStorePublic() tokenStore { return newKeyringStore() }

func ForgetStoredSigninPublic() error {
	return forgetStoredSignin(newKeyringStore())
}
