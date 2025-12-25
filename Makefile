# Makefile

PROJECT := dns-ag
CMDS := nsd rpf

.PHONY: build-all $(addprefix build-,$(CMDS)) $(addprefix run-,$(CMDS)) $(addprefix install-,$(CMDS))

build-all: $(addprefix build-,$(CMDS))

$(addprefix build-,$(CMDS)): build-%:
	go build -o build/$* ./cmd/$*/*.go

$(addprefix run-,$(CMDS)): run-%: build-%
	touch etc/$*.yaml
	build/$* etc/$*.yaml

$(addprefix install-,$(CMDS)): install-%: build-%
	@mkdir -p ~/.local/share/$(PROJECT)
	install -m 755 build/$* ~/.local/share/$(PROJECT)/$*
	@mkdir -p ~/.config/$(PROJECT)
	install -m 644 etc/$*.yaml ~/.config/$(PROJECT)/$*.yaml
