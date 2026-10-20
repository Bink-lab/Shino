package dump

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	steam "gitee.com/zhReimu/steam-go"
	"gitee.com/zhReimu/steam-go/application/apps"
	steamcontent "gitee.com/zhReimu/steam-go/application/content/directory"
	"gitee.com/zhReimu/steam-go/application/unified"
	"gitee.com/zhReimu/steam-go/domain/keyvalues"
	"gitee.com/zhReimu/steam-go/domain/steamid"
	protocol "gitee.com/zhReimu/steam-go/infrastructure/protocol"
	steampb "gitee.com/zhReimu/steam-go/infrastructure/protocol/protobuf"
	steamlang "gitee.com/zhReimu/steam-go/infrastructure/protocol/steamlang"

	"gitee.com/zhReimu/steam-go/application/content/depot"
)

type Client struct {
	raw       *steam.CompositeClient
	http      *http.Client
	servers   []steamcontent.Server
	serversMu sync.Mutex
	tokens    map[string]string
	tokenMu   sync.Mutex
	cmMu      sync.Mutex
	lastCM    time.Time
}

type cdnServer struct {
	url  string
	host string
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second}
}

const manifestVersion = 5

const maxManifestBytes = 128 << 20

var errCDNDenied = errors.New("cdn access denied")

const cmPaceInterval = 700 * time.Millisecond

func (c *Client) paceCM(ctx context.Context) bool {
	c.cmMu.Lock()
	wait := cmPaceInterval - time.Since(c.lastCM)
	if wait < 0 {
		wait = 0
	}
	c.lastCM = time.Now().Add(wait)
	c.cmMu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
	}
	return true
}

func eresultFromError(err error) (steamlang.EResult, bool) {
	var rpcErr *unified.UnifiedRPCError
	if errors.As(err, &rpcErr) {
		return rpcErr.EResult, true
	}
	return steamlang.EResult_OK, false
}

func transientResult(result steamlang.EResult) bool {
	switch result {
	case steamlang.EResult_RateLimitExceeded,
		steamlang.EResult_LimitExceeded,
		steamlang.EResult_ServiceUnavailable,
		steamlang.EResult_Busy,
		steamlang.EResult_Timeout,
		steamlang.EResult_TryAnotherCM,
		steamlang.EResult_AccountLoginDeniedThrottle,
		steamlang.EResult_RemoteCallFailed,
		steamlang.EResult_NoConnection:
		return true
	}
	return false
}

func IsAccessDenied(err error) bool {
	if errors.Is(err, errCDNDenied) {
		return true
	}
	if result, ok := eresultFromError(err); ok {
		return result == steamlang.EResult_AccessDenied
	}
	return false
}

func transientError(err error) bool {
	if errors.Is(err, errCDNDenied) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if result, ok := eresultFromError(err); ok {
		return transientResult(result)
	}
	return true
}

func paceSleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func buildEndpoints(servers []steamcontent.Server, appID uint32) []cdnServer {
	var out []cdnServer
	for _, s := range servers {
		if s.Host == "" || s.SteamChinaOnly {
			continue
		}
		if len(s.AllowedAppIDs) > 0 {
			allowed := false
			for _, id := range s.AllowedAppIDs {
				if id == appID {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		host := s.VHost
		if host == "" {
			host = s.Host
		}
		scheme := "http"
		if s.Port == 443 {
			scheme = "https"
		}
		out = append(out, cdnServer{url: scheme + "://" + host, host: s.Host})
		if len(out) >= 6 {
			break
		}
	}
	return out
}

func (c *Client) Close() {
	if c != nil && c.raw != nil {
		c.raw.Disconnect()
	}
}

func (c *Client) SteamID() string {
	return strconv.FormatUint(c.raw.SteamID().Uint64(), 10)
}

func (c *Client) CellID() uint32 {
	return c.raw.Auth.CellID()
}

func logOnAnonymous(ctx context.Context, raw *steam.CompositeClient) error {
	if err := raw.Auth.LogOnAnonymous(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-raw.Events():
			switch e := ev.(type) {
			case *steam.LoggedOnEvent:
				if e.Result == steamlang.EResult_OK {
					return nil
				}
				return logonResultError(e.Result)
			case *steam.LogOnFailedEvent:
				return logonResultError(e.Result)
			}
		}
	}
}

func (c *Client) LogOnToken(ctx context.Context, username, token string) error {
	logon := new(steampb.CMsgClientLogon)
	logon.AccountName = new(username)
	logon.AccessToken = new(token)
	language := "english"
	logon.ClientLanguage = new(language)
	logon.ProtocolVersion = new(steamlang.MsgClientLogon_CurrentProtocol)
	osType := int32(clientOSType())
	logon.ClientOsType = new(uint32(osType))
	logon.CellId = new(uint32(0))
	logon.ClientPackageVersion = new(uint32(1771))
	logon.SupportsRateLimitResponse = new(true)
	logon.MachineId = machineID()
	logon.DeprecatedObfustucatedPrivateIp = new(uint32(0))
	c.raw.SetSteamIDUint64(steamid.New(0, 0, int32(steamlang.EUniverse_Public), int32(steamlang.EAccountType_Individual)).Uint64())
	if err := c.raw.Send(protocol.NewClientMsgProtobuf(steamlang.EMsg_ClientLogon, logon)); err != nil {
		return err
	}
	_, err := waitLoggedOn(ctx, c.raw)
	return err
}

func waitConnected(ctx context.Context, raw *steam.CompositeClient) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-raw.Events():
			switch ev.(type) {
			case *steam.ConnectedEvent:
				return nil
			case *steam.LogOnFailedEvent:
				return fmt.Errorf("connection rejected")
			}
		}
	}
}

func connectRaw(ctx context.Context) (*steam.CompositeClient, error) {
	raw := steam.NewClient()
	raw.Connect()
	if err := waitConnected(ctx, raw); err != nil {
		raw.Disconnect()
		return nil, err
	}
	return raw, nil
}

func logonResultError(result steamlang.EResult) error {
	switch result {
	case steamlang.EResult_OK:
		return nil
	case steamlang.EResult_InvalidPassword:
		return fmt.Errorf("sign-in rejected, please scan and approve again")
	case steamlang.EResult_AccountNotFound:
		return fmt.Errorf("account not found (use your Steam account name, not your email address)")
	case steamlang.EResult_AccountLogonDenied:
		return fmt.Errorf("steam guard email code required")
	case steamlang.EResult_InvalidLoginAuthCode:
		return fmt.Errorf("steam guard email code invalid, try again")
	case steamlang.EResult_AccountLogonDeniedNoMail:
		return fmt.Errorf("steam guard email code required but no email was sent")
	case steamlang.EResult_ExpiredLoginAuthCode:
		return fmt.Errorf("steam guard email code expired, try again")
	case steamlang.EResult_AccountLockedDown:
		return fmt.Errorf("account locked, unlock it via the Steam store and try again")
	case steamlang.EResult_AccountLogonDeniedVerifiedEmailRequired:
		return fmt.Errorf("verify your email address first, then try again")
	case steamlang.EResult_AccountLoginDeniedNeedTwoFactor:
		return fmt.Errorf("steam guard mobile code required")
	case steamlang.EResult_TwoFactorCodeMismatch:
		return fmt.Errorf("steam guard mobile code mismatch, try again")
	case steamlang.EResult_AccountLoginDeniedThrottle, steamlang.EResult_RateLimitExceeded:
		return fmt.Errorf("too many login attempts, wait a few minutes and try again")
	default:
		return fmt.Errorf("logon failed with result %d", int(result))
	}
}

func waitLoggedOn(ctx context.Context, raw *steam.CompositeClient) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case ev := <-raw.Events():
			switch e := ev.(type) {
			case *steam.LoggedOnEvent:
				if e.Result == steamlang.EResult_OK {
					return raw.SteamID().String(), nil
				}
				return "", logonResultError(e.Result)
			case *steam.LogOnFailedEvent:
				return "", logonResultError(e.Result)
			case *steam.MachineAuthUpdateEvent:
				_ = e.Hash
			}
		}
	}
}

func (c *Client) WaitLicenses(ctx context.Context, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(c.raw.Apps.GetOwnedLicenses()) > 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case ev := <-c.raw.Events():
			if _, ok := ev.(*apps.LicenseListEvent); ok {
				return
			}
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (c *Client) Licenses() []apps.License {
	return c.raw.Apps.GetOwnedLicenses()
}

type OwnedContent struct {
	Apps     []uint32
	Depots   map[uint32]bool
	Complete bool
}

func (c *Client) OwnedContent(ctx context.Context, logf func(format string, args ...any)) (OwnedContent, error) {
	content := OwnedContent{Depots: map[uint32]bool{}}
	log := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	packages := c.raw.Apps.GetOwnedPackageIDs()
	if len(packages) == 0 {
		return content, fmt.Errorf("no licenses received from steam")
	}
	log("%d packages on account", len(packages))
	seen := map[uint32]bool{}
	var firstErr error
	deniedTotal, missingTotal, infosTotal := 0, 0, 0
	shapeLogged := false
	for i, chunk := range chunkUint32(packages, 20) {
		if i > 0 && !paceSleep(ctx, 400*time.Millisecond) {
			break
		}
		if !c.paceCM(ctx) {
			break
		}
		tokens, err := c.raw.Apps.PICSGetAccessTokensSync(ctx, nil, chunk)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log("package tokens %d/%d failed: %v", i+1, (len(packages)+19)/20, err)
			continue
		}
		deniedTotal += len(tokens.PackageDeniedTokens)
		tokenByPackage := map[uint32]uint64{}
		for _, t := range tokens.PackageAccessTokens {
			tokenByPackage[t.GetPackageid()] = t.GetAccessToken()
		}
		var reqs []apps.PICSRequest
		for _, id := range chunk {
			reqs = append(reqs, apps.NewPICSRequestWithToken(id, tokenByPackage[id]))
		}
		infos, err := c.fetchPackages(ctx, reqs)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			log("package info %d/%d failed: %v", i+1, (len(packages)+19)/20, err)
			continue
		}
		infosTotal += len(infos)
		for _, info := range infos {
			if info.MissingToken {
				missingTotal++
			}
			if !shapeLogged && info.KeyValues != nil {
				shapeLogged = true
				names := make([]string, 0, len(info.KeyValues.Children))
				for name := range info.KeyValues.Children {
					names = append(names, name)
				}
				sort.Strings(names)
				log("package %d fields: %s", info.ID, strings.Join(names, ","))
			}
			for _, id := range PackageAppIDs(info.KeyValues) {
				if !seen[id] {
					seen[id] = true
					content.Apps = append(content.Apps, id)
				}
			}
			for _, id := range PackageDepotIDs(info.KeyValues) {
				content.Depots[id] = true
			}
		}
	}
	content.Complete = firstErr == nil && deniedTotal == 0 && missingTotal == 0 && infosTotal == len(packages)
	log("tokens denied: %d, redacted: %d, infos: %d", deniedTotal, missingTotal, infosTotal)
	log("%d depot ids in owned packages", len(content.Depots))
	sort.Slice(content.Apps, func(i, j int) bool { return content.Apps[i] < content.Apps[j] })
	if len(content.Apps) == 0 && firstErr != nil {
		return content, firstErr
	}
	return content, nil
}

func (c *Client) fetchPackages(ctx context.Context, reqs []apps.PICSRequest) (map[uint32]*apps.PICSProductInfo, error) {
	merged := map[uint32]*apps.PICSProductInfo{}
	for i, chunk := range chunkPICS(reqs, 20) {
		if i > 0 && !paceSleep(ctx, 400*time.Millisecond) {
			return merged, ctx.Err()
		}
		if !c.paceCM(ctx) {
			return merged, ctx.Err()
		}
		job := c.raw.Apps.PICSGetProductInfo(nil, chunk, false)
		for {
			timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
			raw, err := c.raw.AwaitJob(timeout, job)
			cancel()
			if err != nil {
				return nil, err
			}
			ev, ok := raw.(*apps.PICSProductInfoResponseEvent)
			if !ok {
				return nil, fmt.Errorf("unexpected response %T", raw)
			}
			for id, info := range ev.ParsedPackages {
				merged[id] = info
			}
			if !ev.ResponsePending {
				break
			}
		}
	}
	return merged, nil
}

func (c *Client) fetchApps(ctx context.Context, reqs []apps.PICSRequest) (map[uint32]*apps.PICSProductInfo, error) {
	merged := map[uint32]*apps.PICSProductInfo{}
	var firstErr error
	for i, chunk := range chunkPICS(reqs, 20) {
		if i > 0 && !paceSleep(ctx, 400*time.Millisecond) {
			break
		}
		if !c.paceCM(ctx) {
			break
		}
		job := c.raw.Apps.PICSGetProductInfo(chunk, nil, false)
		for {
			timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
			raw, err := c.raw.AwaitJob(timeout, job)
			cancel()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				break
			}
			ev, ok := raw.(*apps.PICSProductInfoResponseEvent)
			if !ok {
				if firstErr == nil {
					firstErr = fmt.Errorf("unexpected response %T", raw)
				}
				break
			}
			for id, info := range ev.ParsedApps {
				merged[id] = info
			}
			if !ev.ResponsePending {
				break
			}
		}
	}
	for id, info := range merged {
		if info == nil || !info.UseHTTP || info.HTTPURL == "" {
			continue
		}
		kv, err := c.fetchAppInfoHTTP(ctx, info.HTTPURL)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			delete(merged, id)
			continue
		}
		info.KeyValues = kv
	}
	return merged, firstErr
}

func (c *Client) fetchAppInfoHTTP(ctx context.Context, url string) (*keyvalues.KeyValues, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("appinfo request failed with status %s", resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > 32<<20 {
		return nil, fmt.Errorf("appinfo exceeds size limit")
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	text, err := io.ReadAll(io.LimitReader(gz, 64<<20+1))
	if err != nil {
		return nil, err
	}
	if int64(len(text)) > 64<<20 {
		return nil, fmt.Errorf("appinfo exceeds size limit")
	}
	return keyvalues.ParseText(text)
}

func (c *Client) AppAccessTokens(ctx context.Context, appIDs []uint32) map[uint32]uint64 {
	out := map[uint32]uint64{}
	for i, chunk := range chunkUint32(appIDs, 20) {
		if i > 0 && !paceSleep(ctx, 400*time.Millisecond) {
			break
		}
		if !c.paceCM(ctx) {
			break
		}
		tokens, err := c.raw.Apps.PICSGetAccessTokensSync(ctx, chunk, nil)
		if err != nil {
			continue
		}
		for _, t := range tokens.AppAccessTokens {
			out[t.GetAppid()] = t.GetAccessToken()
		}
	}
	return out
}

func (c *Client) DepotKey(ctx context.Context, depotID, appID uint32) (string, steamlang.EResult, error) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}
	var lastResult steamlang.EResult
	var lastErr error
	for attempt := 0; attempt < len(backoffs)+1; attempt++ {
		if attempt > 0 {
			if !paceSleep(ctx, backoffs[attempt-1]) {
				return "", lastResult, ctx.Err()
			}
		}
		if !c.paceCM(ctx) {
			return "", lastResult, ctx.Err()
		}
		job := c.raw.Apps.GetDepotDecryptionKey(depotID, appID)
		timeout, cancel := context.WithTimeout(ctx, 20*time.Second)
		raw, err := c.raw.AwaitJob(timeout, job)
		cancel()
		if err != nil {
			lastErr = err
			lastResult = steamlang.EResult_OK
			if result, ok := eresultFromError(err); ok {
				lastResult = result
				if !transientResult(result) {
					return "", result, err
				}
			}
			if attempt == len(backoffs) {
				return "", lastResult, lastErr
			}
			continue
		}
		ev, ok := raw.(*apps.DepotKeyResponseEvent)
		if !ok {
			lastErr = fmt.Errorf("unexpected response %T", raw)
			lastResult = steamlang.EResult_OK
			if attempt == len(backoffs) {
				return "", lastResult, lastErr
			}
			continue
		}
		if ev.Result == steamlang.EResult_OK {
			if len(ev.DecryptionKey) == 0 {
				return "", ev.Result, fmt.Errorf("empty key")
			}
			return hex.EncodeToString(ev.DecryptionKey), ev.Result, nil
		}
		lastResult = ev.Result
		lastErr = nil
		if !transientResult(ev.Result) {
			return "", ev.Result, nil
		}
		if attempt == len(backoffs) {
			return "", ev.Result, nil
		}
	}
	return "", lastResult, lastErr
}

func (c *Client) ensureServers(ctx context.Context) ([]steamcontent.Server, error) {
	c.serversMu.Lock()
	defer c.serversMu.Unlock()
	if len(c.servers) > 0 {
		return c.servers, nil
	}
	servers, err := c.raw.Content.GetServersForSteamPipe(ctx, c.CellID(), 20)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("no cdn hosts")
	}
	c.servers = servers
	return servers, nil
}

func (c *Client) cdnToken(ctx context.Context, appID, depotID uint32, host string) (string, error) {
	key := fmt.Sprintf("%d/%s", depotID, host)
	c.tokenMu.Lock()
	token, ok := c.tokens[key]
	c.tokenMu.Unlock()
	if ok {
		return token, nil
	}
	if !c.paceCM(ctx) {
		return "", ctx.Err()
	}
	token, _, err := c.raw.Content.GetCDNAuthToken(ctx, appID, depotID, host)
	if err != nil {
		return "", err
	}
	c.tokenMu.Lock()
	if c.tokens == nil {
		c.tokens = map[string]string{}
	}
	c.tokens[key] = token
	c.tokenMu.Unlock()
	return token, nil
}

func (c *Client) manifestBytes(ctx context.Context, target, token string) ([]byte, int, error) {
	if token != "" {
		separator := "?"
		if strings.Contains(target, "?") {
			separator = "&"
		}
		target += separator + strings.TrimPrefix(token, "?")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > maxManifestBytes {
		return nil, 0, fmt.Errorf("manifest exceeds size limit")
	}
	return body, resp.StatusCode, nil
}

func decodeManifestBody(body []byte) ([]byte, error) {
	if _, err := depot.Deserialize(body); err == nil {
		return body, nil
	}
	if len(body) < 4 || string(body[:4]) != "PK\x03\x04" {
		return nil, fmt.Errorf("manifest invalid: unrecognized format")
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("manifest invalid: bad zip")
	}
	if len(zr.File) != 1 {
		return nil, fmt.Errorf("manifest invalid: zip holds %d files", len(zr.File))
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return nil, fmt.Errorf("manifest invalid: bad zip")
	}
	defer rc.Close()
	inner, err := io.ReadAll(io.LimitReader(rc, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("manifest invalid: bad zip")
	}
	if int64(len(inner)) > maxManifestBytes {
		return nil, fmt.Errorf("manifest invalid: exceeds size limit")
	}
	if _, err := depot.Deserialize(inner); err != nil {
		return nil, fmt.Errorf("manifest invalid: %w", err)
	}
	return inner, nil
}

func manifestRequestCode(ctx context.Context, c *Client, depotID, appID uint32, gid uint64) (uint64, error) {
	backoffs := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
	var lastErr error
	for attempt := 0; attempt < len(backoffs)+1; attempt++ {
		if attempt > 0 {
			if !paceSleep(ctx, backoffs[attempt-1]) {
				return 0, fmt.Errorf("manifest code failed: %w", ctx.Err())
			}
		}
		if !c.paceCM(ctx) {
			return 0, fmt.Errorf("manifest code failed: %w", ctx.Err())
		}
		code, err := c.raw.Content.GetManifestRequestCode(ctx, depotID, appID, gid, "", "")
		if err == nil {
			return code, nil
		}
		lastErr = err
		if result, ok := eresultFromError(err); ok && !transientResult(result) {
			return 0, fmt.Errorf("manifest code failed: %w", err)
		}
		if !transientError(err) {
			return 0, fmt.Errorf("manifest code failed: %w", err)
		}
	}
	return 0, fmt.Errorf("manifest code failed: %w", lastErr)
}

func (c *Client) FetchManifest(ctx context.Context, appID, depotID uint32, gid uint64) ([]byte, error) {
	servers, err := c.ensureServers(ctx)
	if err != nil {
		return nil, err
	}
	hosts := buildEndpoints(servers, appID)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no cdn hosts serve app %d", appID)
	}
	code, err := manifestRequestCode(ctx, c, depotID, appID, gid)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("depot/%d/manifest/%d/%d", depotID, gid, manifestVersion)
	if code != 0 {
		path += fmt.Sprintf("/%d", code)
	}
	var last error
	denied := false
	for _, host := range hosts {
		target := host.url + "/" + path
		body, status, err := c.manifestBytes(ctx, target, "")
		if err != nil {
			last = err
			continue
		}
		if status == 401 || status == 403 {
			token, terr := c.cdnToken(ctx, appID, depotID, host.host)
			if terr != nil {
				last = terr
				continue
			}
			body, status, err = c.manifestBytes(ctx, target, token)
			if err != nil {
				last = err
				continue
			}
			if status == 401 || status == 403 {
				denied = true
				continue
			}
		}
		if status < 200 || status >= 300 {
			if status == 401 || status == 403 {
				denied = true
				continue
			}
			last = fmt.Errorf("cdn %d", status)
			continue
		}
		decoded, err := decodeManifestBody(body)
		if err != nil {
			last = err
			continue
		}
		return decoded, nil
	}
	if last != nil {
		return nil, last
	}
	if denied {
		return nil, fmt.Errorf("cdn 403: %w", errCDNDenied)
	}
	last = fmt.Errorf("manifest unavailable")
	return nil, last
}

func chunkUint32(in []uint32, size int) [][]uint32 {
	var out [][]uint32
	for len(in) > 0 {
		n := size
		if n > len(in) {
			n = len(in)
		}
		out = append(out, in[:n])
		in = in[n:]
	}
	return out
}

func chunkPICS(in []apps.PICSRequest, size int) [][]apps.PICSRequest {
	var out [][]apps.PICSRequest
	for len(in) > 0 {
		n := size
		if n > len(in) {
			n = len(in)
		}
		out = append(out, in[:n])
		in = in[n:]
	}
	return out
}
