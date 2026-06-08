GIT_HEAD = $(shell git rev-parse HEAD | head -c8)

LIMA_INSTANCE = wings
LIMA_CONFIG = $(CURDIR)/lima-wings.yaml

.PHONY: lima-create lima-setup lima-status lima-logs lima-stop lima-delete

lima-create:
	limactl create --name=$(LIMA_INSTANCE) --tty=false $(LIMA_CONFIG)
	limactl start $(LIMA_INSTANCE)

lima-setup:
	chmod +x scripts/lima-setup.sh
	./scripts/lima-setup.sh

lima-status:
	@echo "Instance:"
	@limactl list $(LIMA_INSTANCE) 2>/dev/null || echo "  (not created — run: make lima-create)"
	@echo ""
	@limactl shell $(LIMA_INSTANCE) -- bash -c '\
		echo "VM IP:    $$(hostname -I | awk "{print \$$1}")"; \
		echo "Wings:    $$(systemctl is-active wings 2>/dev/null || echo not-installed)"; \
		curl -sf http://127.0.0.1:8080/api/system 2>/dev/null | head -c 200 || echo "API: not reachable"' \
		2>/dev/null || true

lima-logs:
	limactl shell $(LIMA_INSTANCE) -- journalctl -u wings -n 50 --no-pager

lima-stop:
	limactl stop $(LIMA_INSTANCE)

lima-delete:
	limactl delete $(LIMA_INSTANCE) -f

build:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(pwd)" -o build/wings_linux_amd64 -v wings.go
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(pwd)" -o build/wings_linux_arm64 -v wings.go

debug:
	go build -ldflags="-X github.com/realmctl/wings/system.Version=$(GIT_HEAD)"
	sudo ./wings --debug --ignore-certificate-errors --config config.yml --pprof --pprof-block-rate 1

# Runs a remotly debuggable session for Wings allowing an IDE to connect and target
# different breakpoints.
rmdebug:
	go build -gcflags "all=-N -l" -ldflags="-X github.com/realmctl/wings/system.Version=$(GIT_HEAD)" -race
	sudo dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./wings -- --debug --ignore-certificate-errors --config config.yml

cross-build: clean build compress

clean:
	rm -rf build/wings_*


.PHONY: all build compress clean