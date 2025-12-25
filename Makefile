# Makefile

rpf:
	go build -o build/rpf ./cmd/rpf

rpf-run: rpf
	@if [ ! -f etc/rpf.yaml ]; then \
		echo "Warning: etc/rpf.yaml not found, using sample config"; \
		cp cmd/rpf/rpf.sample.yaml etc/rpf.yaml; \
	fi
	build/rpf etc/rpf.yaml

rpf-install: rpf
	install -m 755 build/rpf ~/.local/bin/rpf
	@mkdir -p ~/.config/rpf
	@if [ -f etc/rpf.yaml ]; then \
		install -m 644 etc/rpf.yaml ~/.config/rpf/rpf.yaml; \
	else \
		install -m 644 cmd/rpf/rpf.sample.yaml ~/.config/rpf/rpf.yaml; \
	fi

.PHONY: rpf rpf-run rpf-install