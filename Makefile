export GOTOOLCHAIN ?= go1.26.5

.PHONY: test build release acceptance publish-npm test-npm
test:
	go test ./...
	go vet ./...
build:
	CGO_ENABLED=0 go build -trimpath -o dist/trace-dock ./cmd/trace-dock
release:
	go run ./cmd/trace-dock-release
publish-npm:
	node dist/npm/publish.cjs
test-npm:
	TRACE_DOCK_TEST_NPM=1 go test ./cmd/trace-dock-release -run TestNPMInstall -count=1 -v
acceptance:
	@test -n "$(MANIFEST)" || (echo "用法: make acceptance MANIFEST=/path/to/manifest.json"; exit 2)
	@if { test -n "$(BASELINE)" || test -n "$(RESTORED)" || test -n "$(SESSION)"; } && { test -z "$(BASELINE)" || test -z "$(RESTORED)" || test -z "$(SESSION)"; }; then echo "BASELINE、RESTORED、SESSION 必须同时提供"; exit 2; fi
	go run ./cmd/trace-dock-acceptance "$(MANIFEST)" $(if $(BASELINE),--baseline "$(BASELINE)") $(if $(RESTORED),--restored "$(RESTORED)") $(if $(SESSION),--session "$(SESSION)")
