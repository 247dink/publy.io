SRC=$(wildcard go.* *.go)
GOPATH=$(shell go env GOPATH)


/usr/bin/virtualenv:
	sudo apt-get install python3-virtualenv


publy.io: ${SRC}
	go build -o publy.io


build: publy.io


.venv/touchfile: requirements.txt /usr/bin/virtualenv
	test -d .venv || virtualenv .venv
	. .venv/bin/activate; pip install -r requirements.txt
	touch .venv/.touchfile


.venv: .venv/.touchfile


lint:
	go vet main.go


test: publy.io .venv
	. .venv/bin/activate; python3 -m unittest tests/test_*.py


image:
	docker build --target=prod .


clean:
	rm -rf publy.io .venv
