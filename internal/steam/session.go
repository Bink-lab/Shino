package steam

import (
	"context"
	"fmt"
	"time"

	steam "gitee.com/zhReimu/steam-go"
	steamcontent "gitee.com/zhReimu/steam-go/application/content/directory"
	"gitee.com/zhReimu/steam-go/infrastructure/protocol/steamlang"
)

type Session struct {
	client *steam.CompositeClient
}

func ConnectAnonymous(ctx context.Context) (*Session, error) {
	c := steam.NewClient()
	c.Connect()
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			c.Disconnect()
			return nil, ctx.Err()
		case ev := <-c.Events():
			switch e := ev.(type) {
			case *steam.ConnectedEvent:
				if err := c.Auth.LogOnAnonymous(); err != nil {
					c.Disconnect()
					return nil, err
				}
			case *steam.LoggedOnEvent:
				if e.Result != steamlang.EResult_OK {
					c.Disconnect()
					return nil, fmt.Errorf("logon result %v", e.Result)
				}
				return &Session{client: c}, nil
			case *steam.LogOnFailedEvent:
				c.Disconnect()
				return nil, fmt.Errorf("anonymous logon failed")
			}
		}
	}
}

func (s *Session) Servers(ctx context.Context) ([]steamcontent.Server, error) {
	return s.client.Content.GetServersForSteamPipe(ctx, s.client.Auth.CellID(), 20)
}

func (s *Session) CDNToken(ctx context.Context, appID, depotID uint32, host string) (string, error) {
	token, _, err := s.client.Content.GetCDNAuthToken(ctx, appID, depotID, host)
	return token, err
}

func (s *Session) FetchDepotOS(ctx context.Context, appID uint32) (map[uint32]string, error) {
	return fetchDepotOSLists(ctx, s.client, appID)
}

func (s *Session) Close() {
	s.client.Disconnect()
}
