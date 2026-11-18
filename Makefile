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
EXTENSIONS := modal-editor-ext-line modal-editor-ext-script modal-editor-ext-dap modal-editor-ext-lsp modal-editor-ext-acp modal-editor-ext-mcp
# cgo を使う標準の拡張機能。C コンパイラが要る。cgo を使うのはこれだけで、本体は CGO_ENABLED=0 で作る（NFR-02）。
CGO_EXTENSIONS := modal-editor-ext-treesitter
# cgo の拡張機能は、musl を C の標準ライブラリとして静的にリンクする（NFR-01）。
# 動的にリンクすると、ビルドしたマシンより古い glibc の環境（AlmaLinux 8 の glibc 2.28 など）や、
# musl の環境（Alpine）で起動できない。
# glibc を静的にリンクすると LGPL の再リンクの義務を伴うので、MIT の musl を使う（docs/measurements/nfr-dist.md）。
# musl-gcc は Debian と Ubuntu の musl-tools にある。
#
# req: NFR-01
MUSL_CC            ?= musl-gcc
CGO_STATIC_LDFLAGS := -linkmode external -extldflags "-static"
# 配布物に同梱する musl の著作権とライセンスの文面。musl-tools が依存する musl のパッケージが置く。
MUSL_COPYRIGHT     ?= /usr/share/doc/musl/copyright
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/piplupflipper/modal-editor/internal/version.version=$(VERSION)

# 配布物（NFR-01、NFR-06）。ビルド済みの実行ファイルと宣言ファイルを 1 つのディレクトリと tar.gz にまとめる。
# 宣言ファイルのある拡張機能は extensions/<名前>/ に実行ファイルと並べ（FR-13）、
# 宣言ファイルの無い実行ファイル（スクリプトの実行環境）は本体と同じ階層に置く。
# 配布するのはビルド済みの実行ファイルだけなので、利用者は C コンパイラも Go も要しない。
# 実行ファイルに静的にリンクするサードパーティのライセンスの文面は licenses/ に置く。
#
# req: NFR-01, NFR-06
DIST_ROOT := dist
DIST_NAME := modal-editor-$(VERSION)-$(shell go env GOOS)-$(shell go env GOARCH)
DIST_DIR  := $(DIST_ROOT)/$(DIST_NAME)
# 配布する実行ファイルのパッケージ。依存するモジュールのライセンスの文面を集めるのに使う。
DIST_PACKAGES := ./cmd/modal-editor ./cmd/modal-editor-mcp-bridge $(addprefix ./cmd/,$(EXTENSIONS) $(CGO_EXTENSIONS))
# 本体にまだつないでいないが、つなぐことが決まっているパッケージ。ライセンスの文面を集める対象に足す。
# internal/diff は go-udiff（BSD-3-Clause と MIT。LICENSE-BSD と LICENSE-MIT）を使い、本体の server から使う（基本設計 6.8）。
# 本体が import すると DIST_PACKAGES の依存に含まれるので、そのときにここから外してよい（残しても結果は変わらない）。
DIST_LICENSE_PACKAGES := $(DIST_PACKAGES) ./internal/diff
# 配布物を起動して確かめるコンテナのイメージ。ビルド用の道具が無く、C の標準ライブラリが異なるものを選ぶ。
# 結果が docs/measurements/nfr-dist.md の記録と同じものを指すように、版のタグで固定する。
DIST_SMOKE_IMAGES := debian:13.7-slim almalinux:8.10-minimal alpine:3.24.1 busybox:1.36.1

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

# 本体を CGO_ENABLED=0 で作る（NFR-02）。make build-check が検査する。
# 本体はシンボルとデバッグ情報を削らずに作る（-ldflags に -s -w を足さない）。デバッガーで変数を読めるようにするためで、
# NFR-05 の実行ファイルの大きさの上限（10 MiB）は、この形で満たす。make build-check と make footprint-check が検査する。
#
# req: NFR-02, NFR-05
.PHONY: build
build: ## 本体、MCP の中継コマンド、標準の拡張機能の実行ファイルを bin/ に作る
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/modal-editor
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(MCP_BRIDGE) ./cmd/modal-editor-mcp-bridge
	for e in $(EXTENSIONS); do CGO_ENABLED=0 go build -trimpath -o $(BIN_DIR)/$$e ./cmd/$$e || exit 1; done
	for e in $(CGO_EXTENSIONS); do CGO_ENABLED=1 CC=$(MUSL_CC) go build -trimpath -ldflags '$(CGO_STATIC_LDFLAGS)' -o $(BIN_DIR)/$$e ./cmd/$$e || exit 1; done

.PHONY: build-check
build-check: build ## make build の生成物を検査する（本体の静的リンクと CGO_ENABLED=0、本体の大きさなど。NFR-02、NFR-05）
	MODAL_EDITOR_BUILD_DIR=$(abspath $(BIN_DIR)) go test -count=1 -v ./internal/distcheck

# 起動から最初の画面までの時間と本体の RSS を、make build の本体で測る（NFR-05）。
# テストの実行ファイルは -race と -cover の計測を含むので測らない。測り方は docs/measurements/nfr05-footprint.md に合わせる。
# 他のテストと CPU を取り合わないよう、このテストだけを単独で実行する。
.PHONY: footprint-check
footprint-check: build ## make build の本体の起動時間と RSS を検査する（10,000 行のファイルで 20 回の中央値。NFR-05）
	MODAL_EDITOR_BUILD_DIR=$(abspath $(BIN_DIR)) go test -count=1 -v -run '^TestFootprintInPTY$$' ./cmd/modal-editor

.PHONY: dist
dist: build ## 配布物を dist/ に作る（実行ファイルと宣言ファイルのディレクトリと tar.gz。NFR-01）
	rm -rf $(DIST_ROOT)
	mkdir -p $(DIST_DIR)/extensions $(DIST_DIR)/licenses/go $(DIST_DIR)/licenses/musl
	cp $(BINARY) $(MCP_BRIDGE) config.example.jsonc $(DIST_DIR)/
	for e in $(EXTENSIONS) $(CGO_EXTENSIONS); do \
		if [ -f cmd/$$e/manifest.json ]; then \
			d=$(DIST_DIR)/extensions/$${e#modal-editor-ext-}; \
			mkdir -p $$d && cp $(BIN_DIR)/$$e cmd/$$e/manifest.json $$d/ || exit 1; \
		else \
			cp $(BIN_DIR)/$$e $(DIST_DIR)/ || exit 1; \
		fi; \
	done
	cp $$(go env GOROOT)/LICENSE $$(go env GOROOT)/PATENTS $(DIST_DIR)/licenses/go/
	cp $(MUSL_COPYRIGHT) $(DIST_DIR)/licenses/musl/copyright
	go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Dir}}{{end}}{{end}}' $(DIST_LICENSE_PACKAGES) | sort -u | \
		while read -r p d; do \
			mkdir -p $(DIST_DIR)/licenses/$$p && cp $$d/LICENSE* $(DIST_DIR)/licenses/$$p/ || exit 1; \
		done
	tar -C $(DIST_ROOT) -czf $(DIST_DIR).tar.gz $(DIST_NAME)

.PHONY: dist-check
dist-check: dist ## 配布物の形を検査する（宣言ファイルの command が実行ファイルを指し、静的にリンクされていること）
	MODAL_EDITOR_DIST_DIR=$(abspath $(DIST_DIR)) go test -count=1 ./internal/distcheck

.PHONY: dist-smoke-images
dist-smoke-images: ## make dist-smoke のイメージを取得する（取得の失敗は 1 回だけ再試行する）
	for img in $(DIST_SMOKE_IMAGES); do docker pull -q $$img || { sleep 10; docker pull -q $$img; } || exit 1; done

.PHONY: dist-smoke
dist-smoke: dist ## ビルド用の道具の無いコンテナで配布物を起動する（Docker が要る。NFR-01、NFR-06）
	for img in $(DIST_SMOKE_IMAGES); do \
		echo "== $$img"; \
		docker run --rm --network none \
			-v $(abspath $(DIST_DIR)):/opt/modal-editor:ro \
			-v $(abspath internal/distcheck/testdata/smoke.sh):/smoke.sh:ro \
			$$img /bin/sh /smoke.sh /opt/modal-editor || exit 1; \
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
