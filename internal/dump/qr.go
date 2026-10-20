package dump

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	steamauth "gitee.com/zhReimu/steam-go/application/auth/modern"
	"gitee.com/zhReimu/steam-go/application/auth/modern/authpb"
	"github.com/skip2/go-qrcode"
)

func RenderQR(text string) ([]string, error) {
	code, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	grid := code.Bitmap()
	size := len(grid)
	at := func(x, y int) bool {
		if x < 0 || y < 0 || x >= size || y >= size {
			return false
		}
		return grid[y][x]
	}
	var out []string
	for y := -1; y <= size; y += 2 {
		var b strings.Builder
		for x := -1; x <= size; x++ {
			top := at(x, y)
			bottom := at(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		out = append(out, b.String())
	}
	return out, nil
}

type tokenClaims struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience []string `json:"aud"`
	Expiry   int64    `json:"exp"`
}

func checkRefreshToken(token string) (*tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("refresh token is malformed")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("refresh token is malformed")
	}
	var claims tokenClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, fmt.Errorf("refresh token is malformed")
	}
	if claims.Issuer != "steam" {
		return nil, fmt.Errorf("refresh token issuer %q is not steam", claims.Issuer)
	}
	found := false
	for _, aud := range claims.Audience {
		if aud == "client" {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("refresh token audience %q cannot log in to steam", claims.Audience)
	}
	if claims.Expiry > 0 && time.Now().Unix() > claims.Expiry {
		return nil, fmt.Errorf("refresh token is expired")
	}
	if len(claims.Subject) != 17 || !strings.HasPrefix(claims.Subject, "7656") {
		return nil, fmt.Errorf("refresh token subject %q is not a steam id", claims.Subject)
	}
	return &claims, nil
}

func claimsSummary(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "malformed"
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "malformed"
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return "malformed"
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		value := fmt.Sprintf("%v", all[key])
		if len(value) > 44 {
			value = value[:20] + "..." + fmt.Sprintf("(%d)", len(value))
		}
		fmt.Fprintf(&b, "%s=%s", key, value)
	}
	b.WriteString("}")
	return b.String()
}

func ConnectQR(ctx context.Context, onChallenge func(url string), onStatus func(line string), onSave func(account, refreshToken string)) (*Client, error) {
	status := func(line string) {
		if onStatus != nil {
			onStatus(line)
		}
	}
	status("connecting to steam")
	qrConn, err := connectRaw(ctx)
	if err != nil {
		return nil, err
	}
	if err := logOnAnonymous(ctx, qrConn); err != nil {
		qrConn.Disconnect()
		return nil, err
	}
	auth := steamauth.New(qrConn.Client)
	auth.SetUnifiedMessenger(qrConn.Unified)
	session, err := auth.BeginAuthSessionViaQR(ctx, steamauth.AuthSessionDetails{
		DeviceFriendlyName: "Shino",
		PlatformType:       steamauth.PlatformTypeSteamClient,
		ClientOSType:       clientOSType(),
		WebsiteID:          "Client",
	})
	if err != nil {
		qrConn.Disconnect()
		return nil, err
	}
	challenge := session.ChallengeURL()
	if challenge == "" {
		qrConn.Disconnect()
		return nil, fmt.Errorf("qr login failed to start")
	}
	if onChallenge != nil {
		onChallenge(challenge)
	}
	interval := session.PollingInterval()
	if interval < time.Second || interval > 30*time.Second {
		interval = 5 * time.Second
	}
	clientID := session.ClientID()
	requestID := session.RequestID()
	for {
		rpcCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		req := &authpb.CAuthentication_PollAuthSessionStatus_Request{
			ClientId:  new(clientID),
			RequestId: requestID,
		}
		resp := &authpb.CAuthentication_PollAuthSessionStatus_Response{}
		err := qrConn.Unified.CallWithContext(rpcCtx, "Authentication.PollAuthSessionStatus#1", req, resp, 30*time.Second)
		cancel()
		if err != nil {
			qrConn.Disconnect()
			return nil, err
		}
		if resp.GetNewClientId() != 0 {
			clientID = resp.GetNewClientId()
		}
		if fresh := resp.GetNewChallengeUrl(); fresh != "" && fresh != challenge {
			challenge = fresh
			if onChallenge != nil {
				onChallenge(challenge)
			}
		}
		if resp.GetRefreshToken() == "" {
			select {
			case <-ctx.Done():
				qrConn.Disconnect()
				return nil, ctx.Err()
			case <-time.After(interval):
			}
			continue
		}
		account := resp.GetAccountName()
		if account == "" {
			qrConn.Disconnect()
			return nil, fmt.Errorf("qr login approved but no account name returned")
		}
		claims, err := checkRefreshToken(resp.GetRefreshToken())
		if err != nil {
			qrConn.Disconnect()
			return nil, err
		}
		if onSave != nil {
			onSave(account, resp.GetRefreshToken())
		}
		sameConn := &Client{raw: qrConn, http: defaultHTTPClient()}
		status("approval received, signing in...")
		raw, cerr := connectRaw(ctx)
		if cerr != nil {
			qrConn.Disconnect()
			return nil, cerr
		}
		client := &Client{raw: raw, http: defaultHTTPClient()}
		if err := logOnWithTimeout(ctx, client, account, resp.GetRefreshToken()); err == nil {
			qrConn.Disconnect()
			return client, nil
		} else {
			freshErr := err
			status("retrying on the same connection...")
			if err := logOnWithTimeout(ctx, sameConn, account, resp.GetRefreshToken()); err == nil {
				raw.Disconnect()
				return sameConn, nil
			}
			raw.Disconnect()
			qrConn.Disconnect()
			return nil, fmt.Errorf("%w (sub=%s aud=%v claims=%s)", freshErr, claims.Subject, claims.Audience, claimsSummary(resp.GetRefreshToken()))
		}
	}
}

func logOnWithTimeout(ctx context.Context, client *Client, username, token string) error {
	tokCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return client.LogOnToken(tokCtx, username, token)
}
