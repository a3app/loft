package web

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/a3app/loft/server/panel/store"
)

// memberproto.go keeps which extra protocols a network member may use
// (VLESS / Trojan / Shadowsocks) besides the loft overlay. The flags live
// in the panel's settings.json, not in network.conf: loft-node's parser
// still rejects unknown config member keys.

var (
	memberProtoID  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	memberProtoOK  = map[string]bool{"loft": true, "vless": true, "trojan": true, "ss": true}
	memberProtoOrd = []string{"loft", "vless", "trojan", "ss"}
)

type memberProtoOut struct {
	Members map[string][]string `json:"members"`
}

type memberProtoIn struct {
	ID     string   `json:"id"`
	Proto  []string `json:"proto"`
	Delete bool     `json:"delete"`
}

func (s *Server) memberProtoGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.Settings.Get()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, memberProtoOut{Members: copyMemberProto(st.MemberProto)})
}

func (s *Server) memberProtoSet(w http.ResponseWriter, r *http.Request) {
	var in memberProtoIn
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	id := strings.TrimSpace(in.ID)
	if !memberProtoID.MatchString(id) {
		fail(w, http.StatusBadRequest, "member id: latin letters, digits, . _ -; up to 64")
		return
	}
	auditTarget(r, id)
	proto, err := normMemberProto(in.Proto)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	st, err := s.Settings.Update(func(st *store.Settings) {
		if st.MemberProto == nil {
			st.MemberProto = map[string][]string{}
		}
		if in.Delete {
			delete(st.MemberProto, id)
			if len(st.MemberProto) == 0 {
				st.MemberProto = nil
			}
			return
		}
		st.MemberProto[id] = proto
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, memberProtoOut{Members: copyMemberProto(st.MemberProto)})
}

func normMemberProto(in []string) ([]string, error) {
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !memberProtoOK[p] {
			return nil, fmt.Errorf("unknown protocol %q", p)
		}
		seen[p] = true
	}
	out := make([]string, 0, len(seen))
	for _, p := range memberProtoOrd {
		if seen[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

func copyMemberProto(in map[string][]string) map[string][]string {
	out := map[string][]string{}
	if in == nil {
		return out
	}
	for id, p := range in {
		out[id] = slices.Clone(p)
	}
	return out
}
