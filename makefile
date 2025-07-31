tidy:
	go mod tidy

fmt:
	go fmt ./...

run: fmt tidy
	go run .

add: fmt tidy
	git add .

.PHONY: webui

webui: 
	cd webui && pnpm run build