// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
)

// The «Прокси» screen saved channels and nothing read them.
//
// Found on 11 October 2026 while preparing real tests: every channel a person
// added in the panel was stored and counted — the budget line even multiplied
// its ceiling by their addresses — and the collector went out through the
// machine's own address regardless, because the only egress it ever read was a
// -proxies file on the command line. The screen promised a rate the collector
// never used and an exit it never took.
//
// This file is the reading: the enabled channels, built into what each
// transport dials. Modelled on WB_GO's internal/engine/channels.go, which went
// through the same lesson first.

// egress is what the configured channels amount to for one transport.
type egress struct {
	// channels are for BlankTrail mode.
	channels []blanktrail.Channel
	// upstreams are for direct mode: proxy URLs, "" meaning this machine.
	upstreams []string
	// exits is how many distinct exits that is.
	exits int
	close func()
}

// listRefresh is how often a list read from a URL is re-read. A provider's
// list changes under it — that is why it is a URL and not a file — and an hour
// is what WB_GO settled on.
const listRefresh = time.Hour

// egressFor builds the enabled channels, plus a -proxies file when one was
// given, for the transport the installation chose.
//
// A channel that will not build stops collection rather than being skipped.
// Skipping it collects through whatever is left — and when that is nothing, it
// is the machine's own address, the one outcome somebody who configured proxies
// was trying to avoid. The same rule, and the same reason, as WB_GO.
func egressFor(ctx context.Context, s *store.Store, blankTrail bool, proxyFile string) (egress, error) {
	var out egress
	var closers []func()
	out.close = func() {
		for _, c := range closers {
			c()
		}
	}
	fail := func(err error) (egress, error) {
		out.close()
		return egress{}, err
	}

	rows, err := s.Channels(ctx)
	if err != nil {
		return egress{}, err
	}
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		switch row.Kind {
		case "direct":
			out.channels = append(out.channels, blanktrail.NewDirectChannel(row.Name))
			out.upstreams = append(out.upstreams, "")
			out.exits++

		case "gateway":
			if !blankTrail {
				return fail(fmt.Errorf("выход «%s» — шлюз VPN BlankTrail, а сбор идёт "+
					"напрямую: шлюзы работают только через BlankTrail. Переключите способ "+
					"выхода в настройках или выключите этот выход", row.Name))
			}
			names := gatewayNames(row.Source)
			if len(names) == 0 {
				return fail(fmt.Errorf("в выходе «%s» не отмечено ни одного шлюза", row.Name))
			}
			out.channels = append(out.channels, blanktrail.NewGatewayChannel(row.Name, names...))
			out.exits += len(names)

		case "proxy-list":
			ups, closer, err := listOf(ctx, s, row)
			if err != nil {
				return fail(fmt.Errorf("выход «%s» не собрался: %w", row.Name, err))
			}
			if closer != nil {
				closers = append(closers, closer)
			}
			out.channels = append(out.channels, blanktrail.NewListChannel(row.Name, ups.rotor))
			for _, u := range ups.list {
				out.upstreams = append(out.upstreams, u.URL())
			}
			out.exits += len(ups.list)

		case "rotating":
			ps, err := s.Proxies(ctx, row.ID)
			if err != nil || len(ps) != 1 {
				return fail(fmt.Errorf("в выходе «%s» должен быть ровно один адрес", row.Name))
			}
			up := upstreamOf(ps[0])
			out.channels = append(out.channels, blanktrail.NewRotatingChannel(row.Name, up,
				row.RotateURL, time.Duration(row.RotateMinIntervalSec)*time.Second))
			out.upstreams = append(out.upstreams, up.URL())
			out.exits++

		default:
			return fail(fmt.Errorf("вид выхода %q этой сборке неизвестен", row.Kind))
		}
	}

	if proxyFile != "" {
		ups, err := readProxies(ctx, proxyFile)
		if err != nil {
			return fail(err)
		}
		out.channels = append(out.channels,
			blanktrail.NewListChannel("pool", blanktrail.NewStaticRotor(ups)))
		for _, u := range ups {
			out.upstreams = append(out.upstreams, u.URL())
		}
		out.exits += len(ups)
	}
	return out, nil
}

type listed struct {
	rotor *blanktrail.Rotor
	list  []blanktrail.Upstream
}

// listOf is a list channel's addresses: the parsed rows when the list was
// pasted, the source re-read on a schedule when it was given as a link or a
// file. The link used to be stored and never fetched by anything.
func listOf(ctx context.Context, s *store.Store, row store.Channel) (listed, func(), error) {
	ps, err := s.Proxies(ctx, row.ID)
	if err != nil {
		return listed{}, nil, err
	}
	if len(ps) > 0 {
		ups := make([]blanktrail.Upstream, 0, len(ps))
		for _, p := range ps {
			if p.Enabled {
				ups = append(ups, upstreamOf(p))
			}
		}
		if len(ups) == 0 {
			return listed{}, nil, fmt.Errorf("все адреса списка выключены")
		}
		return listed{rotor: blanktrail.NewStaticRotor(ups), list: ups}, nil, nil
	}
	src := strings.TrimSpace(row.Source)
	if src == "" {
		return listed{}, nil, fmt.Errorf("список пуст и ссылка не задана")
	}
	kind := "file"
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		kind = "url"
	}
	source := blanktrail.Source{Kind: kind, Location: src, Refresh: listRefresh, DefaultScheme: "socks5"}
	rotor, err := blanktrail.NewRotor(ctx, source)
	if err != nil {
		return listed{}, nil, err
	}
	ups, _, _ := source.Load(ctx)
	return listed{rotor: rotor, list: ups}, rotor.Close, nil
}

func upstreamOf(p store.Proxy) blanktrail.Upstream {
	return blanktrail.Upstream{
		Scheme: p.Scheme, Host: p.Host, Port: strconv.Itoa(p.Port),
		User: p.Username, Pass: p.Password,
	}
}

// gatewayNames reads a gateway channel's source: one name, or several
// separated by commas or lines.
func gatewayNames(source string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(source, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
