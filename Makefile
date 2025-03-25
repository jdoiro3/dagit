.DEFAULT_GOAL := build

.PHONY:fmt vet build
fmt:
	go fmt ./...

vet: fmt
	go vet ./...

build: vet
	yarn --cwd ./nextjs export
	go build -o ~/go/bin/dagit