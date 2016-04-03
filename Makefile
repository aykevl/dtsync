
all:
	go install ./dtsync

packages=./tree/memory ./tree/file ./tree/remote ./dtdiff ./sync ./dtsync

test:
	go test $(packages)

fmt:
	go fmt ./tree $(packages)

vet:
	go vet ./tree $(packages)
