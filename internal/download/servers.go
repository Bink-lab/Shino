package download

import (
	"fmt"
	"sort"
	"strings"

	steamcontent "gitee.com/zhReimu/steam-go/application/content/directory"
)

type Endpoint struct {
	URLHost string
	TokenHost string
	Scheme string
	Port int
	UseAsProxy bool
	ProxyTemplate string
	WeightedLoad float32
	NumEntries int32
	Type string
}

func (e Endpoint) baseHost() string {
	if e.Port != 0 && ((e.Scheme == "http" && e.Port != 80) || (e.Scheme == "https" && e.Port != 443)) {
		return fmt.Sprintf("%s:%d", e.URLHost, e.Port)
	}
	return e.URLHost
}

func (e Endpoint) Display() string {
	return fmt.Sprintf("%s://%s", e.Scheme, e.baseHost())
}

func schemeFor(s steamcontent.Server) string {
	if s.Protocol == steamcontent.ProtocolHTTPS {
		return "https"
	}
	if s.Protocol == steamcontent.ProtocolHTTP {
		if s.Port == 443 {
			return "https"
		}
		return "http"
	}
	if s.Port == 443 {
		return "https"
	}
	return "http"
}

func BuildEndpoints(servers []steamcontent.Server, appID uint32) ([]Endpoint, *Endpoint) {
	var proxy *Endpoint
	for _, s := range servers {
		if s.Host == "" {
			continue
		}
		if s.UseAsProxy && s.ProxyRequestPathTemplate != "" {
			if proxy == nil {
				urlHost := s.VHost
				if urlHost == "" {
					urlHost = s.Host
				}
				p := &Endpoint{
					URLHost: urlHost,
					TokenHost: s.Host,
					Scheme: schemeFor(s),
					Port: s.Port,
					UseAsProxy: true,
					ProxyTemplate: s.ProxyRequestPathTemplate,
					WeightedLoad: s.WeightedLoad,
					NumEntries: s.NumEntries,
					Type: s.Type,
				}
				proxy = p
			}
			continue
		}
	}
	var filtered []steamcontent.Server
	for _, s := range servers {
		if s.Host == "" || s.SteamChinaOnly || s.UseAsProxy {
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
		filtered = append(filtered, s)
	}
	direct := make([]steamcontent.Server, 0, len(filtered))
	for _, s := range filtered {
		if s.Type == "CDN" || s.Type == "SteamCache" {
			direct = append(direct, s)
		}
	}
	if len(direct) == 0 {
		for _, s := range filtered {
			if strings.TrimSpace(s.Type) == "" {
				direct = append(direct, s)
			}
		}
	}
	if len(direct) == 0 {
		direct = filtered
	}
	sort.Slice(direct, func(i, j int) bool {
		if direct[i].WeightedLoad == direct[j].WeightedLoad {
			return direct[i].NumEntries > direct[j].NumEntries
		}
		return direct[i].WeightedLoad < direct[j].WeightedLoad
	})
	var out []Endpoint
	for _, s := range direct {
		urlHost := s.VHost
		if urlHost == "" {
			urlHost = s.Host
		}
		out = append(out, Endpoint{
			URLHost: urlHost,
			TokenHost: s.Host,
			Scheme: schemeFor(s),
			Port: s.Port,
			WeightedLoad: s.WeightedLoad,
			NumEntries: s.NumEntries,
			Type: s.Type,
		})
		if len(out) >= 8 {
			break
		}
	}
	return out, proxy
}

func buildChunkURL(server Endpoint, proxy *Endpoint, depotID uint32, shaHex, token string) string {
	path := fmt.Sprintf("depot/%d/chunk/%s", depotID, shaHex)
	query := strings.TrimPrefix(token, "?")
	if proxy != nil && proxy.UseAsProxy && proxy.ProxyTemplate != "" {
		template := strings.ReplaceAll(proxy.ProxyTemplate, "%host%", server.URLHost)
		template = strings.ReplaceAll(template, "%path%", "/"+path)
		if !strings.HasPrefix(template, "/") {
			template = "/" + template
		}
		base := fmt.Sprintf("%s://%s%s", proxy.Scheme, proxy.baseHost(), template)
		if query != "" {
			base += "?" + query
		}
		return base
	}
	base := fmt.Sprintf("%s://%s/%s", server.Scheme, server.baseHost(), path)
	if query != "" {
		base += "?" + query
	}
	return base
}
