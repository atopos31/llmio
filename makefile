tidy:
	go mod tidy

fmt:
	go fmt ./...

run: fmt tidy
	go run .