# タスクランナー。`make` または `make help` でターゲットの一覧を表示する。

# 開発ツールはバージョンを固定し、`go run` で実行する。
# 初回の実行時だけ、ツールのビルドに数分かかる。
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.9.0

GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

BIN_DIR := bin
BINARY  := $(BIN_DIR)/modal-editor
# MCP の中継コマンド。MCP のクライアントが標準入出力のサーバーとして起動する（FR-33）。
MCP_BRIDGE := $(BIN_DIR)/modal-editor-mcp-bridge
# 標準の拡張機能。本体とは別の実行ファイルとして作る（NFR-01）。
EXTENSIONS := modal-editor-ext-line modal-editor-ext-script modal-editor-ext-dap modal-editor-ext-lsp
# cgo を使う標準の拡張機能。C コンパイラが要る。cgo を使うのはこれだけで、本体は CGO_ENABLED=0 で作る（NFR-02）。
CGO_EXTENSIONS := modal-editor-ext-treesitter
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/piplupflipper/modal-editor/internal/version.version=$(VERSION)

.DEFAULT_GOAL := help

.PHONY: help
help: ## ターゲットの一覧を表示する
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "} {printf "  %-12s %s\n", $$1, $$2}'

.PHONY: fmt
fmt: ## コードを整形する（gofumpt と goimports）
	$(GOLANGCI_LINT) fmt

.PHONY: fmt-check
fmt-check: ## 整形されていないファイルがあれば失敗する
	$(GOLANGCI_LINT) fmt --diff

.PHONY: typecheck
typecheck: ## 型チェックをする（テストコードを含む全パッケージをコンパイルし、go vet を実行）
	go build ./...
	go vet ./...

.PHONY: lint
lint: ## リントを実行する
	$(GOLANGCI_LINT) run

.PHONY: test
test: ## テストを実行する（データ競合の検出つき）
	go test -race -cover ./...

.PHONY: tidy-check
tidy-check: ## go.mod と go.sum が整理済みでなければ失敗する
	go mod tidy -diff

.PHONY: vuln
vuln: ## 依存ライブラリの既知の脆弱性を調べる
	$(GOVULNCHECK) ./...

.PHONY: progress
progress: ## 要件ごとの進捗（docs/progress.md）を生成する
	go run ./cmd/reqtrace

.PHONY: progress-check
progress-check: ## docs/progress.md が最新でなければ失敗する
	go run ./cmd/reqtrace -check

.PHONY: check
check: fmt-check typecheck lint test tidy-check progress-check ## コミット前の検査を全て実行する（CI と同じ内容）

.PHONY: build
build: ## 本体、MCP の中継コマンド、標準の拡張機能の実行ファイルを bin/ に作る
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/modal-editor
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(MCP_BRIDGE) ./cmd/modal-editor-mcp-bridge
	for e in $(EXTENSIONS); do CGO_ENABLED=0 go build -trimpath -o $(BIN_DIR)/$$e ./cmd/$$e || exit 1; done
	for e in $(CGO_EXTENSIONS); do CGO_ENABLED=1 go build -trimpath -o $(BIN_DIR)/$$e ./cmd/$$e || exit 1; done

.PHONY: build-check
build-check: build ## make build の生成物を検査する（本体の静的リンクと CGO_ENABLED=0 など。NFR-02）
	MODAL_EDITOR_BUILD_DIR=$(abspath $(BIN_DIR)) go test -count=1 ./internal/distcheck

.PHONY: run
run: ## ビルドせずに実行する
	go run ./cmd/modal-editor

.PHONY: hooks
hooks: ## コミット前に make check を実行する Git フックを有効にする
	git config core.hooksPath .githooks

.PHONY: clean
clean: ## ビルドの生成物を削除する
	rm -rf $(BIN_DIR)
