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

# Go 覆盖率门禁：基线 72.7%（2026-10-15），阈值取 70%。
# 阈值 = min(70%, 基线向下取整)：70% 是硬底，基线向下取整是防止代码量
# 增大但覆盖率不变时悄悄跌破底线。改动包不足即红。
COVER_THRESHOLD ?= 70.0

cover:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1
	@COV=$$(go tool cover -func=coverage.out | tail -1 | awk '{print $$NF}' | tr -d '%'); \
	if awk "BEGIN { exit !($$COV+0 < $$COVER_THRESHOLD+0) }"; then \
		echo "覆盖率 $$COV%% 低于阈值 $$COVER_THRESHOLD%%"; \
		exit 1; \
	else \
		echo "覆盖率 $$COV%% 达标（阈值 $$COVER_THRESHOLD%%）"; \
	fi
	@rm -f coverage.out

.PHONY: webui cover

webui:
	cd webui && pnpm install && pnpm run build