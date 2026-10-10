// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/BlankTrail/avito-monitor/blanktrail"
	"github.com/BlankTrail/avito-monitor/internal/store"
	"github.com/BlankTrail/avito-monitor/internal/transport/direct"
)

// «Проверить» on the channels screen used to read the list and count it:
// «15000 в списке, первый 45.227.253.80:14601». Parsing says the lines are
// well-formed, which is not what goes wrong. On the list it was written
// against, four exits in five were intercepting TLS — and the button said
// nothing about it, while every pass through them was refused.
//
// Now it asks: a sample of the list's exits, each opening TLS to the site and
// checking the certificate; every gateway of a set through BlankTrail.

// probed is how many exits of a list one press tries. WB_GO's number, and its
// reason: at a quarter alive, five probes report «dead» about once in four
// presses and twenty about three times in a thousand.
const probed = 20

// probeAtOnce bounds how many go out together.
const probeAtOnce = 5

// probeHost is what an exit is asked to reach.
const probeHost = "m.avito.ru"

func checkChannel(ctx context.Context, s *store.Store, id int64) (string, error) {
	row, err := channelByID(ctx, s, id)
	if err != nil {
		return "", err
	}
	switch row.Kind {
	case "gateway":
		return checkGateways(ctx, s, gatewayNames(row.Source))
	case "direct":
		return checkExits(ctx, []string{""}, 1)
	}
	ups, _, err := listOf(ctx, s, row)
	if err != nil {
		return "", err
	}
	urls := make([]string, 0, len(ups.list))
	for _, u := range ups.list {
		urls = append(urls, u.URL())
	}
	if ups.rotor != nil && row.Kind == "proxy-list" && len(ups.list) > 0 {
		ups.rotor.Close()
	}
	return checkExits(ctx, urls, len(urls))
}

func channelByID(ctx context.Context, s *store.Store, id int64) (store.Channel, error) {
	rows, err := s.Channels(ctx)
	if err != nil {
		return store.Channel{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return r, nil
		}
	}
	return store.Channel{}, errors.New("такого выхода нет")
}

// checkExits probes a sample spread across the list — not its head: a
// provider's file is often sorted, and its dead entries cluster.
func checkExits(ctx context.Context, all []string, total int) (string, error) {
	if len(all) == 0 {
		return "", errors.New("в этом выходе нет ни одного адреса")
	}
	take := min(len(all), probed)
	stride := max(1, len(all)/take)
	var chosen []string
	for i := 0; i < len(all) && len(chosen) < take; i += stride {
		chosen = append(chosen, all[i])
	}

	out := make([]direct.Probe, len(chosen))
	var wg sync.WaitGroup
	gate := make(chan struct{}, probeAtOnce)
	for i, eg := range chosen {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			out[i] = direct.ProbeExit(ctx, eg, probeHost)
		}()
	}
	wg.Wait()

	ok, mitm, dead := 0, 0, 0
	issuer := ""
	for _, p := range out {
		switch p.Verdict {
		case direct.ExitOK:
			ok++
		case direct.ExitIntercepting:
			mitm++
			if issuer == "" {
				issuer = p.Issuer
			}
		default:
			dead++
		}
	}

	var b strings.Builder
	if total == 1 && len(chosen) == 1 {
		switch out[0].Verdict {
		case direct.ExitOK:
			return fmt.Sprintf("Выход достаёт до Авито с настоящим сертификатом, %d мс.",
				out[0].Elapsed.Milliseconds()), nil
		case direct.ExitIntercepting:
			return "", fmt.Errorf("выход подменяет сертификат Авито (%s) — сбор через него "+
				"не пойдёт", issuer)
		}
		return "", fmt.Errorf("выход не довёл соединение: %v", blanktrail.Redact(errString(out[0].Err)))
	}
	fmt.Fprintf(&b, "Проверено %d из %d: настоящий сертификат Авито у %d", len(chosen), total, ok)
	if mitm > 0 {
		fmt.Fprintf(&b, ", подменяют сертификат %d", mitm)
		switch {
		case issuer == "самоподписанный":
			b.WriteString(" (сертификат самоподписанный)")
		case issuer != "":
			fmt.Fprintf(&b, " (подпись «%s»)", issuer)
		}
	}
	if dead > 0 {
		fmt.Fprintf(&b, ", не ответили %d", dead)
	}
	b.WriteString(".")
	if ok == 0 {
		return "", errors.New(b.String() + " Сбор через этот список не пойдёт.")
	}
	if ok < len(chosen) {
		b.WriteString(" Сбор обходит негодные адреса сам: перебирает список до рабочего.")
	}
	return b.String(), nil
}

// checkGateways asks BlankTrail to try every gateway of a set. All of them,
// not a sample: a set is a handful, and a broken one costs every port it is
// handed to.
func checkGateways(ctx context.Context, s *store.Store, names []string) (string, error) {
	if len(names) == 0 {
		return "", errors.New("не отмечено ни одного шлюза")
	}
	cfg, err := s.BlankTrailConfig(ctx)
	if err != nil {
		return "", err
	}
	client, err := blanktrail.NewClient(cfg.ControlURL, cfg.APIKey)
	if err != nil {
		return "", err
	}
	ok, first := 0, ""
	for _, name := range names {
		got, err := client.TestEgress(ctx, blanktrail.Egress{Gateway: name})
		why := ""
		if err != nil {
			why = err.Error()
		} else {
			for check, res := range got {
				if !res.OK && !res.Skipped {
					why = check + ": " + res.Detail
					break
				}
			}
		}
		if why == "" {
			ok++
		} else if first == "" {
			first = name + " — " + why
		}
	}
	text := fmt.Sprintf("Ответили шлюзов: %d из %d.", ok, len(names))
	if first != "" {
		text += " Первый отказ: " + first + "."
	}
	if ok == 0 {
		return "", errors.New(text)
	}
	return text, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
