package adm

// This file reads and sets the server's time zone through timedatectl, for
// the panel's system settings page (admproto ops system.tz and
// system.tz.set).

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/a3app/loft/server/panel/admproto"
)

// Timezone is timedatectl's zone and list. The panel shows times in the
// browser's zone; this one decides when the update window opens.
func (o *Ops) Timezone(ctx context.Context) (admproto.Timezone, error) {
	zone, err := o.exec(ctx, "timedatectl", "show", "--property=Timezone", "--value")
	if err != nil {
		return admproto.Timezone{}, fmt.Errorf("timedatectl: %v", err)
	}
	list, err := o.exec(ctx, "timedatectl", "list-timezones")
	if err != nil {
		return admproto.Timezone{}, fmt.Errorf("timedatectl: %v", err)
	}
	return admproto.Timezone{Zone: strings.TrimSpace(zone), Zones: strings.Fields(list)}, nil
}

// TimezoneSet changes the zone to one timedatectl lists.
func (o *Ops) TimezoneSet(ctx context.Context, req admproto.Timezone) (admproto.Timezone, error) {
	cur, err := o.Timezone(ctx)
	if err != nil {
		return cur, err
	}
	if !slices.Contains(cur.Zones, req.Zone) {
		return admproto.Timezone{}, fmt.Errorf("unknown time zone %q", req.Zone)
	}
	if out, err := o.exec(ctx, "timedatectl", "set-timezone", req.Zone); err != nil {
		return admproto.Timezone{}, fmt.Errorf("timedatectl set-timezone: %v %s", err, strings.TrimSpace(out))
	}
	return o.Timezone(ctx)
}
