# 要件ごとの進捗

<!-- このファイルは make progress が生成する。手で編集しない。 -->

要件定義（[requirements.md](requirements.md)）の ID と、ソースとテストのコメントに書いた注釈を突き合わせた結果である。
状態は注釈の有無だけから決まり、実装が要件を満たしているかどうかは表さない。
注釈の書き方は[基本設計](basic-design.md)の「要件との対応」にある。

## 集計

| 状態 | 件数 | 意味 |
|---|---|---|
| 実装とテスト | 11 | 実装とテストの両方に注釈がある |
| 実装のみ | 0 | 実装に注釈があり、テストに無い |
| テストのみ | 0 | テストに注釈があり、実装に無い |
| 未着手 | 24 | 注釈が無い |
| 合計 | 35 | |

## 一覧

| ID | 状態 | 実装 | テスト | 要件 |
|---|---|---|---|---|
| FR-01 | 未着手 | - | - | ノーマル、インサート、ビジュアル、コマンドラインの各モードを持つ |
| FR-02 | 実装とテスト | [internal/input/command.go:74](../internal/input/command.go#L74)<br>[internal/input/parser.go:69](../internal/input/parser.go#L69)<br>[internal/input/parser.go:107](../internal/input/parser.go#L107) | [internal/input/parser_test.go:22](../internal/input/parser_test.go#L22)<br>[internal/input/parser_test.go:163](../internal/input/parser_test.go#L163)<br>[internal/input/parser_test.go:176](../internal/input/parser_test.go#L176)<br>[internal/input/parser_test.go:209](../internal/input/parser_test.go#L209)<br>[internal/input/parser_test.go:292](../internal/input/parser_test.go#L292) | ノーマルモードで、回数、オペレーター、モーションまたはテキストオブジェクトを組み合わせて編集を指定できる（例: `d2w`、`ci"`） |
| FR-03 | 実装とテスト | [internal/input/builtin.go:23](../internal/input/builtin.go#L23)<br>[internal/input/builtin.go:101](../internal/input/builtin.go#L101) | [internal/input/parser_test.go:22](../internal/input/parser_test.go#L22)<br>[internal/input/parser_test.go:423](../internal/input/parser_test.go#L423) | Vim の標準のモーション、オペレーター、テキストオブジェクトを本体が持つ。対象の一覧は実装の段階で定める |
| FR-04 | 未着手 | - | - | レジスターを持つ |
| FR-05 | 実装とテスト | [internal/input/key.go:78](../internal/input/key.go#L78) | [internal/input/key_test.go:10](../internal/input/key_test.go#L10) | マクロを記録し、再生できる。マクロはキー入力の列として記録する |
| FR-06 | 実装とテスト | [internal/input/command.go:74](../internal/input/command.go#L74) | [internal/input/parser_test.go:22](../internal/input/parser_test.go#L22) | ドットリピートを持つ。解釈済みの編集コマンドを 1 つの値として記録し、その値を再実行する |
| FR-07 | 実装とテスト | [internal/buffer/buffer.go:239](../internal/buffer/buffer.go#L239)<br>[internal/buffer/buffer.go:283](../internal/buffer/buffer.go#L283)<br>[internal/buffer/buffer.go:291](../internal/buffer/buffer.go#L291)<br>[internal/buffer/buffer.go:347](../internal/buffer/buffer.go#L347)<br>[internal/buffer/buffer.go:365](../internal/buffer/buffer.go#L365) | [internal/buffer/buffer_test.go:283](../internal/buffer/buffer_test.go#L283)<br>[internal/buffer/buffer_test.go:358](../internal/buffer/buffer_test.go#L358)<br>[internal/buffer/buffer_test.go:495](../internal/buffer/buffer_test.go#L495) | アンドゥとリドゥを持つ |
| FR-08 | 未着手 | - | - | Ex コマンドは `:w`、`:q`、`:s`、`:g` などの部分集合を持つ。対象の一覧は実装の段階で定める |
| FR-09 | 未着手 | - | - | 正規表現は Go の標準ライブラリ（`regexp`）の文法に従う |
| FR-10 | 未着手 | - | - | 拡張機能は、コンパイルする拡張機能と、スクリプトの拡張機能の 2 種類を用意する。どちらも Go で書く |
| FR-11 | 実装とテスト | [ext/server/server.go:122](../ext/server/server.go#L122)<br>[ext/server/server.go:128](../ext/server/server.go#L128)<br>[ext/server/server.go:136](../ext/server/server.go#L136)<br>[ext/server/server.go:142](../ext/server/server.go#L142)<br>[internal/input/keymap.go:98](../internal/input/keymap.go#L98) | [ext/server/server_test.go:184](../ext/server/server_test.go#L184)<br>[internal/input/parser_test.go:238](../internal/input/parser_test.go#L238)<br>[internal/input/parser_test.go:292](../internal/input/parser_test.go#L292)<br>[internal/input/parser_test.go:344](../internal/input/parser_test.go#L344) | 拡張機能は、モーション、オペレーター、テキストオブジェクト、コマンドを追加できる |
| FR-12 | 実装とテスト | [internal/input/command.go:52](../internal/input/command.go#L52) | [internal/input/parser_test.go:238](../internal/input/parser_test.go#L238) | 拡張機能が追加した操作は、拡張機能の側に特別な処理を書かなくても、ドットリピートとマクロで繰り返せる。本体は、拡張機能の ID、コマンドの ID、引数を記録し、再実行のときに同じ拡張機能へ問い合わせ直す |
| FR-13 | 実装とテスト | [ext/manifest.go:16](../ext/manifest.go#L16)<br>[ext/manifest.go:113](../ext/manifest.go#L113)<br>[ext/manifest.go:182](../ext/manifest.go#L182) | [ext/manifest_test.go:26](../ext/manifest_test.go#L26)<br>[ext/manifest_test.go:56](../ext/manifest_test.go#L56)<br>[ext/manifest_test.go:189](../ext/manifest_test.go#L189)<br>[ext/manifest_test.go:218](../ext/manifest_test.go#L218) | 拡張機能は、追加する操作の一覧と起動の契機を、実行せずに読める宣言ファイルに書く |
| FR-14 | 未着手 | - | - | 拡張機能は、宣言した契機（対応するファイルを開いたとき、コマンドを呼んだとき、など）で初めて起動する。例: 言語サーバーの無いテキストファイルを開いている間は、LSP の拡張機能を起動しない |
| FR-15 | 実装とテスト | [internal/buffer/buffer.go:239](../internal/buffer/buffer.go#L239) | [internal/buffer/buffer_test.go:53](../internal/buffer/buffer_test.go#L53)<br>[internal/buffer/buffer_test.go:239](../internal/buffer/buffer_test.go#L239) | バッファの内容を変更できるのは本体だけとする。拡張機能は、編集の内容と、元にしたバッファのバージョン番号を本体へ送り、本体はバージョン番号が現在と一致した編集だけを適用する |
| FR-16 | 実装とテスト | [ext/server/buffer.go:95](../ext/server/buffer.go#L95)<br>[ext/server/server.go:424](../ext/server/server.go#L424)<br>[internal/buffer/buffer.go:239](../internal/buffer/buffer.go#L239)<br>[internal/buffer/buffer.go:347](../internal/buffer/buffer.go#L347)<br>[internal/buffer/buffer.go:365](../internal/buffer/buffer.go#L365)<br>[internal/buffer/edit.go:64](../internal/buffer/edit.go#L64) | [ext/server/buffer_test.go:36](../ext/server/buffer_test.go#L36)<br>[ext/server/server_test.go:184](../ext/server/server_test.go#L184)<br>[ext/server/server_test.go:218](../ext/server/server_test.go#L218)<br>[ext/server/server_test.go:256](../ext/server/server_test.go#L256)<br>[internal/buffer/buffer_test.go:382](../internal/buffer/buffer_test.go#L382) | 本体は拡張機能へ、最初にバッファの全文を送り、以後は差分を送る |
| FR-17 | 実装とテスト | [internal/rpc/conn.go:237](../internal/rpc/conn.go#L237)<br>[internal/rpc/conn.go:283](../internal/rpc/conn.go#L283)<br>[internal/rpc/conn.go:451](../internal/rpc/conn.go#L451) | [internal/rpc/conn_test.go:269](../internal/rpc/conn_test.go#L269)<br>[internal/rpc/conn_test.go:374](../internal/rpc/conn_test.go#L374) | キー入力ごとに結果が要る操作（拡張機能のモーションなど）は、期限付きで応答を待ち、期限を超えたら操作を失敗させる。期限を超えた応答は、後から届いても適用しない |
| FR-18 | 未着手 | - | - | FR-17 の期限は設定で指定できる。0 を指定した場合は、待たずに次のキー入力を受け付け、結果が届いた時点でバッファのバージョン番号とカーソル位置が問い合わせたときと同じ場合だけ適用する。違う場合は破棄する |
| FR-19 | 未着手 | - | - | マクロとドットリピートの再生中は、FR-18 の設定に関わらず、前の操作の完了を待ってから次の操作へ進む。拡張機能が応答しなければ、その位置で再生を止める |
| FR-20 | 未着手 | - | - | 拡張機能ごとのメモリの上限を、設定で指定できる |
| FR-30 | 未着手 | - | - | LSP のクライアントを持つ |
| FR-31 | 未着手 | - | - | DAP のクライアントを持つ |
| FR-32 | 未着手 | - | - | MCP のサーバーを持つ。開いているバッファ、カーソル位置、選択範囲、診断などの取得と、編集の操作を、ツールとして提供する |
| FR-33 | 未着手 | - | - | MCP のクライアントからの接続は、中継コマンドで受ける。クライアントが中継コマンドを標準入出力の MCP サーバーとして起動し、中継コマンドが起動中のエディタへ Unix ドメインソケットで接続する |
| FR-34 | 未着手 | - | - | ACP のクライアントを持つ。コーディングエージェントを子プロセスとして起動する |
| FR-35 | 未着手 | - | - | tree-sitter による構文解析を持つ。構文ハイライトは、tree-sitter の拡張機能が色を付ける範囲の一覧を本体へ送り、本体は受け取り済みの一覧で描画する |
| FR-36 | 未着手 | - | - | スクリプトの拡張機能を実行する。yaegi は本体には入れず、スクリプトを実行する別の実行ファイルに入れる |
| FR-40 | 未着手 | - | - | 設定ファイルの形式は JSONC とする |
| FR-41 | 未着手 | - | - | 設定ファイルは本体が直接読む。キーマップなどの設定は、拡張機能の起動を待たずに反映する |
| NFR-01 | 未着手 | - | - | メンテナンスされている一般的な Linux ディストリビューションに、ファイルを置くだけで動作する。本体と拡張機能は別々のファイルとして配布する |
| NFR-02 | 未着手 | - | - | 本体は cgo を使わず、C の標準ライブラリに依存しない実行ファイルにする |
| NFR-03 | 未着手 | - | - | 拡張機能の異常終了が本体に波及しない。拡張機能のクラッシュ、panic、無限ループ、デッドロック、期限の超過、設定した上限の範囲でのメモリの使い切りが起きても、本体のプロセスは動き続け、編集中のバッファは失われない |
| NFR-04 | 未着手 | - | - | 拡張機能の読み込みは非同期とする。本体は、拡張機能の読み込みが終わるのを待たずに起動し、キー入力を受け付ける |
| NFR-05 | 未着手 | - | - | 軽量である。起動時間、メモリの使用量、実行ファイルのサイズの目標値は未定で、計測してから定める |
| NFR-06 | 未着手 | - | - | 利用者のマシンに、C コンパイラなどのビルド用の道具を要求しない |
