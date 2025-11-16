mkdb:
	mkdir -p db

tidy:
	go mod tidy

fmt:
	go fmt ./...

run: fmt tidy mkdb
	go run .

add: fmt tidy
	git add .

.PHONY: webui

webui: 
	cd webui && pnpm install && pnpm run build