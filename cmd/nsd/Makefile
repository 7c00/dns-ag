APP_NAME := nsd

build:
	go build -o main

run: build
	touch config.yaml
	./main

install: build
	@mkdir -p ~/.local/bin
	@mkdir -p ~/.config/$(APP_NAME)
	install -m 644 config.yaml ~/.config/$(APP_NAME)/config.yaml
	install -m 755 main ~/.local/bin/$(APP_NAME)

.PHONY: build run install
