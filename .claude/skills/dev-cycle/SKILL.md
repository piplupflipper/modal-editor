---
name: dev-cycle
description: modal-editor の実装サイクル（issue 起票 → worktree で実装 → 検査 → PR → 独立レビュー → マージ）の手順と、セッションごとの反省を織り込んだ規則。オーケストレータが Opus のワーカーへ実装やレビューを委譲するとき、ワーカーが実装を始めるときに読む。
---

# dev-cycle

基本設計（`docs/basic-design.md`）を満たすまで、実装・テスト・レビュー・マージを繰り返すための手順である。
オーケストレータは分割、指示、検証、マージだけを行い、実装とレビューは Opus のワーカーに委譲する。

## 1. 役割

| 役割 | やること | やらないこと |
|---|---|---|
| オーケストレータ | issue の起票、ワーカーの起動、報告の検証、レビュー結果の判定、マージ、この skill の更新 | パッケージの実装 |
| 実装ワーカー | 1 issue を 1 PR にする。worktree で実装し、`make check` を通して PR を作る | 担当外のパッケージの変更、マージ |
| レビューワーカー | PR の差分を、基本設計と要件定義に照らして読み、指摘を返す | コードの変更 |

## 2. 実装ワーカーの手順

### 2.1 準備

- リポジトリは `/home/endo84/ghq/github.com/piplupflipper/modal-editor`（GitHub の `piplupflipper/modal-editor`）。main の作業ツリーは触らない
- 最初に読むもの: この skill、`README.md`、`docs/requirements.md`、`docs/basic-design.md`、担当 issue（`gh issue view <番号>`）
- worktree を作る: リポジトリで `git fetch origin` の後、`gwq add -b <ブランチ名>` を実行し、`gwq get <ブランチ名>` で得たパスへ `cd` する
- ブランチ名は `<verb>/<issue番号>-<slug>`（例: `feat/10-buffer`）

### 2.2 実装の規約

- 基本設計 2 章の層を守る。土台（`internal/buffer`、`internal/rpc`、`ext`）は他のパッケージを import しない。同じ層どうしも import しない
- 編集の状態は、イベントループの goroutine 1 つだけが触る（基本設計 2.1）。土台のパッケージは並行に呼ばれる前提を置かない。ただし `internal/rpc` は内部で goroutine を使うので `-race` で検証する
- コメントと doc コメントは日本語、エラーメッセージとログは英語でも日本語でもよいが、パッケージ内で揃える（既存の `cmd/modal-editor/main.go` は日本語）
- doc コメントは詳細設計を兼ねる（README）。公開する型と関数には、何を表すか・何を保証するかを書く
- 要件を実装・検証した箇所には `req: FR-xx` の注釈を付ける（基本設計 9 章）。注釈を変えたら `make progress` で `docs/progress.md` を生成し直してコミットに含める
- 外部ライブラリを足すのは、issue か裁定で決まったものだけにする。cgo は本体で使わない（NFR-02）
- 設計と食い違う実装が必要になったら、実装を止めずに PR 説明の「設計との差分」節に書く。基本設計の改訂は別 issue にする

### 2.3 検査

- `make check`（整形、型チェック、リント、テスト、`go mod tidy`、progress の一致）を worktree で通す
- 整形で落ちたら `make fmt` を打つ
- 初回の `make lint` は golangci-lint のビルドで数分かかる。フォアグラウンドで待つ

### 2.4 コミットと PR

- コミットメッセージは日本語、件名は `<type>: <要約> (#<issue番号>)`
- コミット末尾にはオーケストレータが指示した attribution の行を付ける
- `git push -u origin <ブランチ名>` の後、`gh pr create --base main --head <ブランチ名> -t "<件名>" -F <説明ファイル> -a piplupflipper` で PR を作る
- PR 説明は日本語で、「変更内容」「設計との差分」「検査結果」「未確認事項」の節を置き、`Closes #<issue番号>` を 1 行で含める。末尾にオーケストレータが指示した 2 行を付ける
- 作成後に `gh pr view <PR番号> --json body --jq .body` で `Closes` が入ったことを確かめる

### 2.5 ワーカーの規則

- Agent ツールで他のエージェントに委譲しない
- 本文はファイルに書いて渡す。heredoc で書くなら `<<'EOF'` にする
- scratchpad に作るファイル名は `<issue番号>-` を接頭辞にする
- `git checkout -- .`、`git restore .`、引数なしの `git stash`、`git reset --hard` を打たない
- 最終報告は、PR の URL、変更ファイル数、`make check` の結果、未確認事項を 5 行以内で書く

## 3. レビューワーカーの手順

- 入力は PR 番号だけにする。オーケストレータの見立ては渡さない（結論に引きずられないため）
- `gh pr diff <番号>` と、関係する設計の節を読む。必要なら PR のブランチを worktree に取り出してテストを動かす（`gwq add <ブランチ名>` は既存ブランチの取り出し）
- 観点: 基本設計と要件定義との食い違い、層の違反、テストが要件を実際に検証しているか、境界値（空のバッファ、最終行、多バイト文字）、並行処理の競合、エラーの握りつぶし
- 指摘は「必須（マージ前に直す）」「推奨（別 issue でよい）」に分け、`path:line` と、失敗する具体的な入力を付ける
- 結果は `gh pr review <番号> --comment -F <ファイル>` で PR に残し、最終報告に必須の指摘の件数と要旨を書く

## 4. オーケストレータの手順

1. 基本設計の未実装部分から issue を切る。同じファイルを触る issue は並列にしない（`docs/progress.md` は例外。後述）
2. 裁定が要る項目は `裁定要` のラベルを付けた issue にまとめ、依存しない issue を先に進める
3. 実装ワーカーを並列で起動する。指示には issue 番号、ブランチ名、attribution の行だけを書き、手順はこの skill を読ませる
4. 報告が来たら、`git diff --stat origin/main...origin/<branch>` で範囲を確かめ、worktree に未コミットの変更が無いか `git -C <worktree> status --short` で見る
5. レビューワーカーを起動する。必須の指摘は実装ワーカーへ `SendMessage` で差し戻す
6. CI（`gh pr checks <番号>`）が緑で、必須の指摘が無ければ `gh pr merge <番号> --merge --delete-branch` でマージする。マージ後に `git log origin/main` で入ったことを確かめる
7. 後続の PR は main へ rebase し、`make progress` を打ち直す（`docs/progress.md` はほぼ全 PR が触るので、毎回ここで衝突する）
8. セッションの終わりに、5 章へ反省を書き足し、規則に織り込めるものは 1〜4 章を直す

## 5. 反省と改善の記録

セッションごとに、起きたこと・原因・次からの規則を書く。
規則に織り込んだら、その旨を書く。

### 2026-11-16（セッション 1）

- 初版を作成した。この時点では実績が無く、1〜4 章は opus-delegation スキル（個人用）の段取りを、このリポジトリ（GitHub、`gh`、Go）に合わせて写したものである
