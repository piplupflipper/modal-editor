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
# 本体を CGO_ENABLED=0 で作る指定は build のターゲットにあり、make build-check が検査する。
#
# req: NFR-02
CGO_EXTENSIONS := modal-editor-ext-treesitter
# cgo の拡張機能は、C の標準ライブラリごと静的にリンクする（NFR-01）。
# 動的にリンクすると、ビルドしたマシンより古い glibc の環境（AlmaLinux 8 の glibc 2.28 など）や、
# musl の環境（Alpine）で起動できない（docs/measurements/nfr-dist.md）。
# netgo と osusergo は、静的にリンクした glibc では動かない名前の解決（getaddrinfo、getpwnam）を Go の実装に置き換える。
#
# req: NFR-01
CGO_STATIC_TAGS    := netgo,osusergo
CGO_STATIC_LDFLAGS := -linkmode external -extldflags "-static"
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/piplupflipper/modal-editor/internal/version.version=$(VERSION)

# 配布物（NFR-01、NFR-06）。ビルド済みの実行ファイルと宣言ファイルを 1 つのディレクトリと tar.gz にまとめる。
# 宣言ファイルのある拡張機能は extensions/<名前>/ に実行ファイルと並べ（FR-13）、
# 宣言ファイルの無い実行ファイル（スクリプトの実行環境）は本体と同じ階層に置く。
# 配布するのはビルド済みの実行ファイルだけなので、利用者は C コンパイラも Go も要しない。
#
# req: NFR-01, NFR-06
DIST_ROOT := dist
DIST_NAME := modal-editor-$(VERSION)-$(shell go env GOOS)-$(shell go env GOARCH)
DIST_DIR  := $(DIST_ROOT)/$(DIST_NAME)
# 配布物を起動して確かめるコンテナのイメージ。ビルド用の道具が無く、C の標準ライブラリが異なるものを選ぶ。
DIST_SMOKE_IMAGES := debian:stable-slim almalinux:8-minimal alpine:3 busybox:stable

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
	for e in $(CGO_EXTENSIONS); do CGO_ENABLED=1 go build -trimpath -tags $(CGO_STATIC_TAGS) -ldflags '$(CGO_STATIC_LDFLAGS)' -o $(BIN_DIR)/$$e ./cmd/$$e || exit 1; done

.PHONY: build-check
build-check: build ## make build の生成物を検査する（本体の静的リンクと CGO_ENABLED=0 など。NFR-02）
	MODAL_EDITOR_BUILD_DIR=$(abspath $(BIN_DIR)) go test -count=1 ./internal/distcheck

.PHONY: dist
dist: build ## 配布物を dist/ に作る（実行ファイルと宣言ファイルのディレクトリと tar.gz。NFR-01）
	rm -rf $(DIST_DIR) $(DIST_DIR).tar.gz
	mkdir -p $(DIST_DIR)/extensions
	cp $(BINARY) $(MCP_BRIDGE) $(DIST_DIR)/
	for e in $(EXTENSIONS) $(CGO_EXTENSIONS); do \
		if [ -f cmd/$$e/manifest.json ]; then \
			d=$(DIST_DIR)/extensions/$${e#modal-editor-ext-}; \
			mkdir -p $$d && cp $(BIN_DIR)/$$e cmd/$$e/manifest.json $$d/ || exit 1; \
		else \
			cp $(BIN_DIR)/$$e $(DIST_DIR)/ || exit 1; \
		fi; \
	done
	tar -C $(DIST_ROOT) -czf $(DIST_DIR).tar.gz $(DIST_NAME)

.PHONY: dist-check
dist-check: dist ## 配布物の形を検査する（宣言ファイルの command が実行ファイルを指し、静的にリンクされていること）
	MODAL_EDITOR_DIST_DIR=$(abspath $(DIST_DIR)) go test -count=1 ./internal/distcheck

.PHONY: dist-smoke
dist-smoke: dist ## ビルド用の道具の無いコンテナで配布物を起動する（Docker が要る。NFR-01、NFR-06）
	for img in $(DIST_SMOKE_IMAGES); do \
		echo "== $$img"; \
		docker run --rm --network none -v $(abspath $(DIST_DIR)):/opt/modal-editor:ro $$img /bin/sh -c '\
			set -e; d=/opt/modal-editor; \
			$$d/modal-editor --version; \
			$$d/modal-editor-mcp-bridge --version; \
			for m in $$d/extensions/*; do $$m/modal-editor-ext-$${m##*/} </dev/null; echo "$${m##*/}: ok"; done; \
			$$d/modal-editor-ext-script 2>&1 | grep -q "^usage:"; echo "script: ok"' || exit 1; \
	done

.PHONY: run
run: ## ビルドせずに実行する
	go run ./cmd/modal-editor

.PHONY: hooks
hooks: ## コミット前に make check を実行する Git フックを有効にする
	git config core.hooksPath .githooks

.PHONY: clean
clean: ## ビルドの生成物を削除する
	rm -rf $(BIN_DIR) $(DIST_ROOT)
