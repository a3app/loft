package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

// The Node screen: node.conf commands, lists, the service log, the token.
// Saving restarts the node (which carries the browser's connection), so
// saves run as jobs.

func (s *Server) nodeCfg(w http.ResponseWriter, r *http.Request) {
	var v admproto.NodeConfig
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpNodeConfig, nil, &v); err != nil {
		admFail(w, err)
		return
	}
	if userOf(r).Role != "owner" && s.Store.Perm(userOf(r).Role, "node") < 2 {
		v.MaskPub = "" // keys and tokens only for those who may change them
	}
	ok(w, v)
}

func (s *Server) nodeCfgSave(w http.ResponseWriter, r *http.Request) {
	var in admproto.NodeConfig
	if !readJSON(w, r, 64<<10, &in) {
		return
	}
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var v admproto.NodeConfig
		if err := s.Adm(by).Call(ctx, admproto.OpNodeSave, in, &v); err != nil {
			return nil, admErr(err)
		}
		return v, nil
	})
}

func (s *Server) nodeLists(w http.ResponseWriter, r *http.Request) {
	var in admproto.NodeLists
	if !readJSON(w, r, 8<<20, &in) {
		return
	}
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var v admproto.NodeConfig
		if err := s.Adm(by).Call(ctx, admproto.OpNodeLists, in, &v); err != nil {
			return nil, admErr(err)
		}
		return v, nil
	})
}

func (s *Server) nodeLog(w http.ResponseWriter, r *http.Request) {
	var v admproto.NodeLog
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpNodeLog, nil, &v); err != nil {
		admFail(w, err)
		return
	}
	ok(w, v)
}

func (s *Server) nodeToken(w http.ResponseWriter, r *http.Request) {
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var v admproto.NodeConfig
		if err := s.Adm(by).Call(ctx, admproto.OpNodeToken, struct{}{}, &v); err != nil {
			return nil, admErr(err)
		}
		return v, nil
	})
}

// CloudflareIPs are Cloudflare's published ranges.
var CloudflareIPs = []string{"https://www.cloudflare.com/ips-v4", "https://www.cloudflare.com/ips-v6"}

// cdnRanges fetches a CDN's address ranges for the CDN block.
func (s *Server) cdnRanges(w http.ResponseWriter, r *http.Request) {
	auditSkip(r)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var out []string
	c := s.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	for _, u := range CloudflareIPs {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := c.Do(req)
		if err != nil {
			fail(w, http.StatusBadGateway, "Cloudflare is not reachable: "+err.Error())
			return
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fail(w, http.StatusBadGateway, fmt.Sprintf("Cloudflare answered HTTP %d", resp.StatusCode))
			return
		}
		for _, ln := range strings.Fields(string(b)) {
			if p, err := netip.ParsePrefix(ln); err == nil {
				out = append(out, p.String())
			}
		}
	}
	if len(out) == 0 {
		fail(w, http.StatusBadGateway, "Cloudflare gave no ranges")
		return
	}
	ok(w, map[string]any{"ranges": out})
}
