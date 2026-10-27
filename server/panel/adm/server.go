package adm

// This file is the front door of loft-panel-adm (the root helper): it accepts
// connections on the admproto unix socket, checks who is calling, decodes the
// one JSON request each connection carries, runs the matching Ops method and
// writes back one JSON reply. admproto (server/panel/admproto) defines the op
// names and message shapes; the real work lives in the other files of this
// package (ops.go, node.go, channels.go, mail.go, backup.go and so on).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

// Server answers admproto requests. Allow vets the peer of each connection
// (on Linux: SO_PEERCRED uid must be the panel's); nil allows everyone and
// is for tests only.
type Server struct {
	Ops   *Ops
	Allow func(net.Conn) error
	Log   *log.Logger
}

// Serve accepts until ln is closed.
func (s *Server) Serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serveConn(c)
	}
}

// opDeadline is how long one op may run; apply restarts the node twice at worst.
func opDeadline(op string) time.Duration {
	switch op {
	case admproto.OpNetworkApply, admproto.OpNodeRestart, admproto.OpDomainSet, admproto.OpCertInstall:
		return 90 * time.Second
	case admproto.OpPackages, admproto.OpPackageUnit, admproto.OpMTProtoGet, admproto.OpMTProtoSet, admproto.OpNetState, admproto.OpNetChange, admproto.OpChannels:
		return 45 * time.Second
	case admproto.OpChannelSave, admproto.OpChannelDelete, admproto.OpChannelSwitch, admproto.OpChannelRules,
		admproto.OpChannelPrio, admproto.OpChannelPrioDelete, admproto.OpDirectCheck,
		admproto.OpNodeSave, admproto.OpNodeLists, admproto.OpNodeToken, admproto.OpRelayKey, admproto.OpCtlRoll,
		admproto.OpMailDomain, admproto.OpMailBox, admproto.OpMailAlias, admproto.OpMailHelo, admproto.OpMailWebmail, admproto.OpMailCerts, admproto.OpMailApply:
		return 90 * time.Second
	case admproto.OpMail, admproto.OpMailStats, admproto.OpSysUsers, admproto.OpSysUserSave, admproto.OpSysUserDelete,
		admproto.OpGuard, admproto.OpGuardSet, admproto.OpGuardBan, admproto.OpGuardUnban,
		admproto.OpBackupKey, admproto.OpBackupState, admproto.OpBackupRun, admproto.OpBackupRestore, admproto.OpBackupDir:
		return 30 * time.Second
	}
	return 15 * time.Second
}

// serveConn handles one connection: read one request, check the peer, run
// the op under its deadline and write one reply. Errors are sent to the
// panel as text and also logged; successful changes are logged too, so the
// system log keeps an audit trail of who changed what.
func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var req admproto.Request
	dec := json.NewDecoder(bufio.NewReader(io.LimitReader(c, admproto.MaxRequest)))
	if err := dec.Decode(&req); err != nil {
		json.NewEncoder(c).Encode(admproto.Reply{Error: "bad request"})
		return
	}
	// After the request, so the client write is not a broken pipe when we refuse.
	if s.Allow != nil {
		if err := s.Allow(c); err != nil {
			s.logf("REFUSED peer: %v", err)
			json.NewEncoder(c).Encode(admproto.Reply{Error: "not allowed"})
			return
		}
	}
	d := opDeadline(req.Op)
	c.SetDeadline(time.Now().Add(d + 5*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	data, err := s.do(ctx, req)
	rep := admproto.Reply{OK: err == nil}
	if err != nil {
		rep.Error = err.Error()
		s.logf("%s by=%q: REFUSED %v", req.Op, req.By, err)
	} else {
		if b, merr := json.Marshal(data); merr == nil {
			rep.Data = b
		}
		if mutating(req.Op) {
			s.logf("%s by=%q: done", req.Op, req.By)
		}
	}
	json.NewEncoder(c).Encode(rep)
}

// mutating reports whether op changes the system. Only those ops are logged
// on success; read-only ops would flood the log because the panel polls them.
func mutating(op string) bool {
	switch op {
	case admproto.OpNetworkApply, admproto.OpMemberKey, admproto.OpNodeRestart, admproto.OpDomainSet, admproto.OpCertInstall,
		admproto.OpPackagesApply, admproto.OpPackageUnit, admproto.OpMTProtoSet, admproto.OpUpdateApply, admproto.OpTimezoneSet,
		admproto.OpNetChange, admproto.OpNetConfirm, admproto.OpNetRevert,
		admproto.OpChannelSave, admproto.OpChannelDelete, admproto.OpChannelSwitch, admproto.OpChannelRules,
		admproto.OpChannelPrio, admproto.OpChannelPrioDelete, admproto.OpDirectCheck,
		admproto.OpNodeSave, admproto.OpNodeLists, admproto.OpNodeToken, admproto.OpRelayKey, admproto.OpCtlRoll,
		admproto.OpMailDomain, admproto.OpMailBox, admproto.OpMailAlias, admproto.OpMailHelo, admproto.OpMailWebmail, admproto.OpMailCerts, admproto.OpMailApply,
		admproto.OpSysUserSave, admproto.OpSysUserDelete, admproto.OpGuardSet, admproto.OpGuardBan, admproto.OpGuardUnban,
		admproto.OpBackupKey, admproto.OpBackupRun, admproto.OpBackupRestore:
		return true
	}
	return false
}

// do dispatches one request to the Ops method for its op and returns that
// method's result, which serveConn marshals into the reply. Ops that take a
// body decode it with strict, so an unknown field is an error rather than
// being silently ignored. Unknown ops are refused. The cases are grouped by
// area in the same order as the op list in admproto; a trailing comment on
// the first case of each group names the area. Channels are upstream links
// (WireGuard, OpenVPN, ZeroTier, SSTP, proxies, Xray) and the rules that
// pick one.
func (s *Server) do(ctx context.Context, req admproto.Request) (any, error) {
	switch req.Op {
	case admproto.OpPing: // health, network.conf (the member list), member keys
		return admproto.Ping{Version: version.Version}, nil
	case admproto.OpNetworkGet:
		return s.Ops.NetworkGet()
	case admproto.OpNetworkApply:
		var b admproto.NetworkApply
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.NetworkApply(ctx, b)
	case admproto.OpMemberKey:
		var b admproto.MemberKey
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MemberKey(b)
	case admproto.OpMemberGet:
		var b admproto.MemberKey
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MemberGet(b)
	case admproto.OpNodeStatus:
		return s.Ops.NodeStatus(ctx), nil
	case admproto.OpNodeRestart:
		return s.Ops.NodeRestart(ctx)
	case admproto.OpNodeInfo:
		return s.Ops.NodeInfo()
	case admproto.OpDomainSet:
		var b admproto.DomainSet
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.DomainSet(ctx, b)
	case admproto.OpCertInstall:
		var b admproto.CertInstall
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.CertInstall(ctx, b)
	case admproto.OpPackages: // packages, MTProto proxy, self-update, jobs
		return s.Ops.Packages(ctx)
	case admproto.OpPackagesApply:
		var b catalog.Plan
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.StartPackages(ctx, req.By, b)
	case admproto.OpPackageUnit:
		var b admproto.PackageUnit
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.PackageUnit(ctx, b)
	case admproto.OpMTProtoGet:
		return s.Ops.MTProtoGet()
	case admproto.OpMTProtoSet:
		var b admproto.MTProtoSet
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MTProtoSet(ctx, b)
	case admproto.OpUpdateApply:
		var b admproto.UpdateApply
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.StartUpdate(ctx, req.By, b)
	case admproto.OpJobGet:
		return s.Ops.JobGet(ctx, true)
	case admproto.OpNetState: // host network; changes undo themselves unless confirmed
		return s.Ops.NetState(ctx)
	case admproto.OpNetChange:
		var b admproto.NetChange
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.NetChange(ctx, b)
	case admproto.OpNetConfirm, admproto.OpNetRevert:
		var b admproto.NetID
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		if req.Op == admproto.OpNetConfirm {
			return nil, s.Ops.NetConfirm(ctx, b.ID)
		}
		return nil, s.Ops.NetRevert(ctx, b.ID)
	case admproto.OpPresence: // live node-status.json from loft-node; missing = nobody yet
		b, err := os.ReadFile(filepath.Join(s.Ops.Dir, "node-status.json"))
		if err != nil {
			return json.RawMessage(`{"members":[],"relays":[]}`), nil
		}
		return json.RawMessage(b), nil
	case admproto.OpNodeConfig:
		return s.Ops.NodeConfig(ctx)
	case admproto.OpNodeSave:
		var b admproto.NodeConfig
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.NodeSave(ctx, b)
	case admproto.OpNodeLists:
		var b admproto.NodeLists
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.NodeLists(ctx, b)
	case admproto.OpNodeLog:
		return s.Ops.NodeLog(ctx)
	case admproto.OpNodeToken:
		return s.Ops.NodeTokenRotate(ctx)
	case admproto.OpRelayKey:
		var b admproto.RelayKeyReq
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.RelayKey(ctx, b)
	case admproto.OpCtlRoll:
		var b admproto.CtlRoll
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ControllerRoll(ctx, b)
	case admproto.OpRelayGet:
		var b admproto.RelayKeyReq
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.RelayGet(b)
	case admproto.OpChannels: // upstream links the node sends traffic out through
		return s.Ops.Channels(ctx)
	case admproto.OpChannelSave:
		var b admproto.Channel
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelSave(ctx, b)
	case admproto.OpChannelDelete:
		var b admproto.ChannelName
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelDelete(ctx, b.Name)
	case admproto.OpChannelSwitch:
		var b admproto.ChannelSwitch
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelSwitch(ctx, b)
	case admproto.OpChannelRules:
		var b admproto.ExitRules
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelRules(ctx, b)
	case admproto.OpChannelPrio:
		var b admproto.Prio
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelPrio(ctx, b)
	case admproto.OpChannelPrioDelete:
		var b admproto.ChannelName
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelPrioDelete(ctx, b.Name)
	case admproto.OpDirectCheck:
		var b admproto.ChanCheck
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.ChannelDirectCheck(ctx, b)
	case admproto.OpMail: // mail server: domains, mailboxes, aliases, certificates
		return s.Ops.Mail(ctx)
	case admproto.OpMailDomain:
		var b admproto.MailDomainSave
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MailDomainSave(ctx, b)
	case admproto.OpMailBox:
		var b admproto.MailBoxSave
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MailBoxSave(ctx, b)
	case admproto.OpMailAlias:
		var b admproto.MailAliasSave
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MailAliasSave(ctx, b)
	case admproto.OpMailHelo:
		var b admproto.MailHeloSave
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MailHeloSave(ctx, b)
	case admproto.OpMailWebmail:
		var b admproto.MailWebmail
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MailWebmail(ctx, b)
	case admproto.OpMailCerts:
		var b admproto.MailCerts
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.MailCerts(ctx, b)
	case admproto.OpMailApply:
		return s.Ops.MailApply(ctx)
	case admproto.OpMailStats:
		return s.Ops.MailStats(ctx)
	case admproto.OpSysUsers: // Linux accounts on the host
		return s.Ops.SysUsers(ctx)
	case admproto.OpSysUserSave:
		var b admproto.SysUserSave
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.SysUserSave(ctx, b)
	case admproto.OpSysUserDelete:
		var b admproto.SysUserDelete
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.SysUserDelete(ctx, b)
	case admproto.OpGuard: // fail2ban settings and bans against password guessing
		return s.Ops.Guard(ctx)
	case admproto.OpGuardSet:
		var b admproto.GuardConf
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.GuardSet(ctx, b)
	case admproto.OpGuardBan, admproto.OpGuardUnban:
		var b admproto.GuardBan
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		if req.Op == admproto.OpGuardBan {
			return s.Ops.GuardBan(ctx, b)
		}
		return s.Ops.GuardUnban(ctx, b)
	case admproto.OpBackupKey: // encrypted backups
		var b admproto.BackupKey
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.BackupKeySet(ctx, b)
	case admproto.OpBackupState:
		return s.Ops.BackupState(ctx)
	case admproto.OpBackupRun, admproto.OpBackupRestore:
		var b admproto.BackupJob
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		if req.Op == admproto.OpBackupRun {
			return s.Ops.BackupRun(ctx, req.By, b)
		}
		return s.Ops.BackupRestore(ctx, req.By, b)
	case admproto.OpBackupDir:
		var b admproto.BackupDir
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.BackupDir(ctx, b)
	case admproto.OpTimezone: // system time zone
		return s.Ops.Timezone(ctx)
	case admproto.OpTimezoneSet:
		var b admproto.Timezone
		if err := strict(req.Body, &b); err != nil {
			return nil, err
		}
		return s.Ops.TimezoneSet(ctx, b)
	}
	return nil, fmt.Errorf("unknown operation %q", req.Op)
}

// strict decodes body and rejects unknown fields, so a panel/daemon version
// mismatch fails loudly instead of silently dropping a field.
func strict(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return errors.New("missing body")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("bad body: %v", err)
	}
	return nil
}

// logf writes to the server log when one is set (tests leave it nil).
func (s *Server) logf(f string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(f, a...)
	}
}
