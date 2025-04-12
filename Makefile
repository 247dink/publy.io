publy.io: publy/*
	go build -C publy -o ../publy.io


build: publy.io


.venv/touchfile: requirements.txt
	test -d .venv || virtualenv .venv
	. .venv/bin/activate; pip install -r requirements.txt
	touch .venv/touchfile


.venv: .venv/touchfile


test: publy.io .venv
	. .venv/bin/activate; python3 -m unittest tests/test_*.py


image:
	docker build --target=prod .


clean:
	rm -rf publy.io
