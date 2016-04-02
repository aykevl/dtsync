
all:
	go install ./dtsync

test:
	go test ./tree/memory ./tree/file ./tree/remote ./dtdiff ./sync ./dtsync

fmt:
	go fmt ./tree/memory ./tree/file ./tree/remote ./dtdiff ./sync ./dtsync

vet:
	go vet ./tree/memory ./tree/file ./tree/remote ./dtdiff ./sync ./dtsync
