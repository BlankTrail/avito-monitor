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
# It greps imports rather than parsing them, which is enough because the
# forbidden list is short and exact, and because a build tag or a dot-import
# clever enough to defeat this would be far more visible in review than the
# import it was hiding.
check-no-net:
	@echo "checking avito/ imports"
	@! grep -REn '^\s*(_ )?"(net|net/http|net/url|database/sql|time|os|os/exec)"' \
		--include='*.go' avito/ \
		| grep -v '_test.go' \
		|| (echo "avito/ must not import net, a database, a clock or the OS" && exit 1)
	@echo "ok"
