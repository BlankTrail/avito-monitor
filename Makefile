# SPDX-License-Identifier: AGPL-3.0-or-later

.PHONY: check check-no-net test vet fmt build

check: fmt vet test check-no-net

fmt:
	@gofmt -l ./avito ./blanktrail ./cmd ./internal

vet:
	go vet ./...

test:
	go test ./...

build:
	go build ./...

# check-no-net is what the spec means by «the domain. No net, no net/http, no
# clock, no database — enforced by make check-no-net».
#
# Enforced and not merely intended. avito/ is what turns bytes into a Page, and
# the moment it can fetch anything the offline tests stop proving that a fixture
# and a live page are read by the same code. The clock is on the list for the
# same reason: an extractor that could ask the time would have a golden fixture
# whose output changed at midnight.
#
# The real enforcement is two tests in the package itself, because those run for
# anybody who types `go test ./...` — which is everybody, and which is the
# difference between a rule and a wish. TestPackageReachesNoNetwork walks
# `go list -deps` and is transitive; TestTheDomainImportsNothingItMustNot parses
# direct imports and catches the clock, which arrives transitively through
# encoding/json and cannot be banned by the first.
#
# This target exists because the spec names this spelling, and it greps, which
# is a third and weakest net.
#
# net/url is deliberately absent from the list, and it was here until the day
# somebody ran this target. It cannot reach anything -- it parses and prints
# strings -- and avito.SearchURL needs it, which the spec calls the whole of the
# entry point, and it is pure. Banning it made `make check` red against a
# package whose own two tests were green: a rule enforcing a mistake.
#
# The two tests are authoritative and this list must match their union.
# TestTheMakefileAndTheTestsBanTheSameThings fails if they drift, because a
# target nobody runs -- there is no `make` on the machine this was written on --
# drifts silently otherwise.
check-no-net:
	@echo "checking avito/ imports"
	@! grep -REn '^\s*(_ )?"(net|net/http|database/sql|time|os|os/exec)"' \
		--include='*.go' avito/ \
		| grep -v '_test.go' \
		|| (echo "avito/ must not import net, a database, a clock or the OS" && exit 1)
	@echo "ok"
