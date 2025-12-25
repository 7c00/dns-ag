# Makefile

rpf:
	go build -o build/rpf cmd/rpf/*.go

rpf-run: rpf
	build/rpf etc/rpf.yaml

rpf-install: rpf
	install -m 755 build/rpf ~/.local/bin/rpf
	install -m 644 etc/rpf.yaml ~/.config/rpf/rpf.yaml

.PHONY: rpf rpf-run rpf-install