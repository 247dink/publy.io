SRC=$(wildcard go.* *.go)
GOPATH=$(shell go env GOPATH)


@{GOPATH}/bin/golangci-lint:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest


publy.io: ${SRC}
	go build -o publy.io


build: publy.io


.venv/touchfile: requirements.txt
	test -d .venv || virtualenv .venv
	. .venv/bin/activate; pip install -r requirements.txt
	touch .venv/touchfile


.venv: .venv/touchfile


lint: ${GOPATH}/bin/golangci-lint
	${GOPATH}/bin/golangci-lint run


test: publy.io .venv
	. .venv/bin/activate; python3 -m unittest tests/test_*.py


image:
	docker build --target=prod .


clean:
	rm -rf publy.io
