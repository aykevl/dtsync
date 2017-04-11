
all: install

install:
	go install ./dtsync

packages=./sync

test:
	GODEBUG=cogocheck=2 go test -timeout 2m $(packages)

test-race:
	GODEBUG=cogocheck=2 go test -race $(packages)

fmt:
	go fmt ./tree $(packages)

vet:
	go vet ./tree $(packages)
