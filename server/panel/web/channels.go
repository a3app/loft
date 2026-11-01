package web

// This file serves the "External channels" tab: upstream ways out of the
// server (WireGuard, proxies, Xray links) that traffic can be sent through.
// The panel itself keeps nothing; every read and change is passed to the
// helper (loft-panel-adm, the root daemon reached through admproto), which
// also stores the channel secrets.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/a3app/loft/server/panel/adm"
	"github.com/a3app/loft/server/panel/admproto"
)

// External channels: everything goes through the helper, which owns the
// secrets; replies carry none.

// channels returns the current channels view from the helper.
func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	var v admproto.ChannelsView
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpChannels, nil, &v); err != nil {
		admFail(w, err)
		return
	}
	ok(w, v)
}

// channelOp passes one change to the helper and answers with the new view.
func channelOp[T any](s *Server, op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req T
		if !readJSON(w, r, 128<<10, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
		defer cancel()
		var v admproto.ChannelsView
		if err := s.Adm(userOf(r).Login).Call(ctx, op, req, &v); err != nil {
			admFail(w, err)
			return
		}
		s.logf("%s by %s", op, userOf(r).Login)
		ok(w, v)
	}
}

// wgKey makes a WireGuard key pair for the channel dialog.
func (s *Server) wgKey(w http.ResponseWriter, r *http.Request) {
	var k [32]byte
	rand.Read(k[:])
	k[0] &= 248
	k[31] = k[31]&127 | 64
	pub, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, map[string]string{"private": base64.StdEncoding.EncodeToString(k[:]), "public": base64.StdEncoding.EncodeToString(pub)})
}

// xrayParse shows what a link says before it is saved.
func (s *Server) xrayParse(w http.ResponseWriter, r *http.Request) {
	auditSkip(r)
	var req struct {
		Link string `json:"link"`
	}
	if !readJSON(w, r, 8<<10, &req) {
		return
	}
	l, err := adm.ParseXrayLink(req.Link)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, l)
}
