
all: install

install:
	go install ./dtsync

packages=./tree/memory ./tree/file ./tree/remote ./dtdiff ./sync ./dtsync

test:
	go test -timeout 2m $(packages)

test-race:
	go test -race $(packages)

fmt:
	go fmt ./tree $(packages)

vet:
	go vet ./tree $(packages)
