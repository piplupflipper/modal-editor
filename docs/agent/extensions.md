<!-- AI エージェント向けの文書（vi-imitation --docs extensions）。印の間は make docs が生成する。印の間を手で編集しない。 -->
# 拡張機能

拡張機能は、本体が子プロセスとして起動する別の実行ファイルである。
本体と拡張機能は、子プロセスの標準入力と標準出力で JSON-RPC 2.0 のメッセージをやり取りする。
メッセージの区切りは LSP と同じ `Content-Length: <バイト数>\r\n\r\n` のヘッダーである。
拡張機能の標準エラー出力は、本体の記録（`--log` のファイル）に 1 行ずつ残る（`vi-imitation --docs debugging`）。

<!-- generated:api-version:begin -->
この本体の拡張機能 API の版は `0.13.0` である（`initialize` の `apiVersion`）。
<!-- generated:api-version:end -->

Go で書くときは、Go のモジュール `github.com/piplupflipper/vi-imitation` の公開パッケージ `ext`（メッセージの型）と `ext/server`（メッセージのループ、バッファの写し、ハンドラーの登録）を使える。
Go のソースを 1 つ置いて動かすスクリプトの拡張機能もある（`vi-imitation --docs usage スクリプトの拡張機能`）。

## 置き場所

本体は起動のときに、次のディレクトリの `<名前>/manifest.json` を読む。
同じ ID の拡張機能が両方にあれば、1 つ目のものを使う。

1. `$XDG_DATA_HOME/vi-imitation/extensions/`（`XDG_DATA_HOME` が空なら `~/.local/share/vi-imitation/extensions/`）
2. 本体の実行ファイルの隣の `extensions/`（配布物の標準の拡張機能）

本体は宣言ファイルを読むだけで、起動の契機（`activation`）が成り立つまで拡張機能を起動しない。
宣言ファイルを変えたら、本体を起動し直す。

```jsonc
{
  "id": "hello",
  // 相対パスは宣言ファイルのディレクトリから解決する。PATH からは探さない
  "command": "./hello",
  "activation": [
    { "on": "command", "id": "hello.say" },
    { "on": "filetype", "pattern": "markdown" },
  ],
  "contributes": {
    "commands": [{ "id": "hello.say", "description": "挨拶をメッセージの行に出す" }],
  },
}
```

利用者の設定ファイルの `extensions.settings.<ID>` は、`initialize` の `settings` で拡張機能に渡る。
`extensions.activation.<ID>` に書いた `filetype` の契機は、宣言ファイルの `activation` に足される。

## 動き

1. 本体は、起動の契機が成り立ったら `command` を `args` と一緒に起動する。作業ディレクトリは本体のものを引き継ぐ
2. 本体は `initialize` を送る。5 秒以内に応答が無ければ異常とみなす
3. 本体は、開いているバッファの全文を `buffer/open` で送り、その後の変更を `buffer/change` で送る
4. 本体は 5 秒ごとに `ping` を送る。2 秒以内に応答が無ければ異常とみなす。時間のかかる処理は、メッセージを読む処理と別に動かす
5. 本体は、エディタを終えるときに `shutdown` を送る。3 秒で終わらなければ SIGTERM を、さらに 1 秒で SIGKILL を送る

異常（プロセスの終了、期限切れ、解釈できないメッセージ）のときは、プロセスグループに SIGKILL を送り、500 ms から 30 秒まで倍に延ばしながら起動し直す。
5 回続けて失敗すると無効にする。
起動し直したときは、開いているバッファの全文を送り直す。
異常と無効は、エディタのメッセージの行と `:messages` に出る。

キー入力ごとに結果が要る問い合わせ（`motion/resolve`、`textObject/resolve`、`operator/apply`）は、期限（設定の `extensions.syncTimeoutMs`。既定は 100 ms）までに応答が無ければ、その操作を失敗にする。
期限を過ぎて届いた応答は適用しない。

## 環境変数

本体は、拡張機能の子プロセスに次の環境変数を渡す。

<!-- generated:env:begin -->
- `VI_IMITATION_EXTENSION_DIR`: 本体が拡張機能の子プロセスに渡す環境変数の名前である（名前は仮）。
  値は、宣言ファイルのあるディレクトリの絶対パスである。
  子プロセスの作業ディレクトリは本体のものを引き継ぐので、宣言ファイルの隣のファイルを探すときはこの値を使う。
  スクリプトの拡張機能の実行ファイルは、args の相対パスのスクリプトをここから解決する。
- `VI_IMITATION_MCP_SOCKET`: 本体がすべての拡張機能の子プロセスに渡す環境変数の名前である（名前は仮）。
  値は、MCP の中継コマンドが接続する Unix ドメインソケットの絶対パスで、本体が起動ごとに一意に決める。
  本体は、起動の契機 ActivationSocket を宣言した拡張機能のために、このパスで起動時から待ち受ける。
  ACP の拡張機能はこのパスを中継コマンドの引数にしてエージェントへ渡す。
- `VI_IMITATION_SOCKET_FDS`: 本体が ActivationSocket の拡張機能へ引き継いだファイル記述子の数を渡す環境変数の名前である（名前は仮）。
  値が n（1 以上）なら、ファイル記述子 SocketFDStart が待ち受けているソケットで、
  SocketFDStart+1 から SocketFDStart+n-1 までが、本体が受け付けて拡張機能へ渡す接続である。
  値が空か変数が無ければ、何も引き継いでいない。
  本体は、すべての拡張機能の子プロセスでこの変数を上書きするので、本体の環境の古い値は引き継がない。
  拡張機能は ext/server の InheritedSocket で受け取る。
<!-- generated:env:end -->

## 宣言ファイル

宣言ファイルの型と、そこから辿れる型である。
型は Go の書き方で書く（`[]T` は配列、`*T` は省ける値、`json.RawMessage` は任意の JSON）。
「省ける」は、書かなくてよい（空の値と同じ）ことを表す。

<!-- generated:manifest:begin -->
#### `Manifest`

拡張機能の宣言ファイルの内容である。
本体は、拡張機能を起動せずに宣言ファイルだけを読み、追加する操作と起動の契機を知る。
項目の名前は仮である。

宣言ファイルは JSONC で書く。
JSONC は internal/config が github.com/tailscale/hujson で標準の JSON に直してから読む。
このパッケージは、読み込んだ後の値の型と検査（Validate）だけを持つ。

- `id`（`string`）: 拡張機能の ID である。ドットリピートの記録と設定で使う。
- `command`（`string`）: 拡張機能の実行ファイルのパスである。
  相対パスは、宣言ファイルのあるディレクトリから解決する。
- `args`（`[]string`、省ける）: Command に渡す引数の列である。
  スクリプトの拡張機能が、共通の実行ファイルにスクリプトのパスを渡すのに使う。無ければ省く。
  本体は解決せずにそのまま渡す。相対パスの引数は、拡張機能が EnvExtensionDir のディレクトリから解決する。
- `saveHook`（`bool`、省ける）: true なら、稼働中のときに保存の直前の buffer/willSave を受け取る。
  保存の前に整形する LSP の拡張機能が使う。
- `activation`（`[]Activation`）: 起動の契機の一覧である。どれか 1 つが成り立った時点で起動する。
- `contributes`（`Contributes`）: 拡張機能が追加する操作である。
- `provides`（`[]Operation`、省ける）: 拡張機能が引き受ける本体の操作（提供者として名乗る操作）の列である。
  本体は、操作が呼ばれたら、カーソルのあるバッファのファイルの種類で提供者を選び、provider/invoke で問い合わせる。
  選び方は次のとおりである（仮）。
    - その操作を名乗った拡張機能のうち、ファイルの種類の契機（ActivationFileType。設定で足したものを含む）を持たないか、
      その Pattern のどれかがバッファのファイルの種類と一致するものを候補にする
    - 候補のうち、未起動か起動中か稼働中のものを、本体が宣言ファイルを読んだ順に探し、最初のものを使う。
      同じ操作を複数の拡張機能が名乗ったときの選び方は未決で、それまでは先に読んだものを使う
    - 未起動なら、問い合わせで起動する。provides だけを持つ拡張機能は、Activation を空にしてよい
  この版の API の知らない操作の名前は、Validate が警告にし、本体は問い合わせない。

#### `Activation`

1 つの起動の契機である。

- `on`（`ActivationKind`）: 契機の種類である。
- `id`（`string`、省ける）: 操作が呼ばれたときの契機で、その操作の ID である。それ以外の契機では空。
- `pattern`（`string`、省ける）: ActivationFileType の契機で、ファイルの種類の名前である。
  ActivationSettingsFileType の契機では、設定の中の値を指す JSON Pointer である。それ以外の契機では空。

#### `ActivationKind`（`string`）

起動の契機の種類である。

- `"operator"`（ActivationOperator）
- `"motion"`（ActivationMotion）
- `"textObject"`（ActivationTextObject）
- `"command"`（ActivationCommand）
- `"filetype"`（ActivationFileType）
- `"socket"`（ActivationSocket）
- `"settingsFileType"`（ActivationSettingsFileType）

#### `Contributes`

拡張機能が追加する操作の一覧である。

- `operators`（`[]Contribution`、省ける）
- `motions`（`[]Contribution`、省ける）
- `textObjects`（`[]Contribution`、省ける）
- `commands`（`[]Contribution`、省ける）
- `statusline`（`[]StatusLinePart`、省ける）: ステータス行とタブ行に足す部品の一覧である。
  部品の中身は statusline/set で送る（MethodStatusLineSet）。足したのは API 0.5.0 である。
- `popupKeys`（`[]Contribution`、省ける）: この拡張機能が popup/open で開いたポップアップの中だけで効くキーである。
  各項目の Keys は空にできない。フォーカスを入れたポップアップのノーマルモードで打つと、本体は view/key で ID を知らせる
  （ViewKeyParams）。本体の既定のキーより先に引く。ID は popupKeys の中で一意にし、他の操作の ID とは別に数える。
  操作ではないので、起動の契機（Activation）の対象にはならない。

#### `Contribution`

拡張機能が追加する 1 つの操作である。

- `id`（`string`）: 操作の ID である。拡張機能の中で一意にする。
  本体はドットリピートで、拡張機能の ID とこの ID を記録する。
- `keys`（`string`、省ける）: 既定のキーの列（"ys" など）である。利用者の設定で上書きできる。
  既定のキーを持たない操作では空。
- `description`（`string`、省ける）: 操作の説明文である。本体はキーの一覧とヘルプ（:help）に出す。無ければ省く。
  足したのは API 0.4.0 の後だが、版は上げていない（APIVersion の doc）。
- `action`（`PopupKeyAction`、省ける）: Contributes.PopupKeys の項目で、キーを打ったときに view/key を送る代わりに本体がする操作である（API 0.10.0 で足した）。
  省くと view/key を送る。ほかの種類の操作では読まない。

#### `PopupKeyAction`（`string`）

contributes.popupKeys のキーで、view/key の代わりに本体がする操作の名前である。

- `"select"`（PopupKeySelect）: 一覧と木の <CR> と同じく、カーソルの行の項目を選ぶ（view/select）。木の子を持つ項目なら開閉する（view/expand）。
  ファイラの l（ディレクトリを開閉し、ファイルを開く。nvim の neo-tree の open）に使う。
  <CR> と同じ経路なので、選んだら閉じる中央のポップアップ（PaneOpenParams.CloseOnSelect）を閉じ、
  分割したペインで選んだ後の editor/open は端末多重化ソフトのフォーカスを元の client へ戻す。

#### `StatusLinePart`

宣言ファイルの contributes.statusline の 1 つの部品である。

- `id`（`string`）: 部品の ID である。拡張機能の ID と "." を前に付け、その後には "." を含めない（拡張機能の ID が "mrreview" なら "mrreview.status"）。
  利用者は、設定の statusline と tabline の left と right にこの ID を書いて並べる。
  本体の部品の ID は "." を含まないので、本体の部品と重ならない。拡張機能の ID は一意で、最後の "." より前が拡張機能の ID に決まるので、
  拡張機能の ID が "." を含んでも、他の拡張機能の部品と重ならない。
- `description`（`string`、省ける）: 部品の説明文である。本体はヘルプとキーの一覧に出さないが、利用者が宣言ファイルを読んで並べる部品を選べるように書く。

#### `Operation`（`string`）

本体の操作の名前である（名前は仮）。
本体は LSP と DAP のプロトコルを知らず、操作の名前と引数だけを提供者へ渡す。

- `"definition"`（OpDefinition）
- `"implementation"`（OpImplementation）
- `"typeDefinition"`（OpTypeDefinition）
- `"hover"`（OpHover）
- `"references"`（OpReferences）
- `"documentSymbols"`（OpDocumentSymbols）
- `"workspaceSymbols"`（OpWorkspaceSymbols）
- `"format"`（OpFormat）
- `"rename"`（OpRename）
- `"codeAction"`（OpCodeAction）
- `"completion"`（OpCompletion）: 名前だけを決めた操作である。補完の取り決めは未決で、本体はまだ問い合わせない。
- `"breakpoint.toggle"`（OpBreakpointToggle）
- `"breakpoint.conditional"`（OpBreakpointConditional）
- `"breakpoint.clear"`（OpBreakpointClear）
- `"debug.continue"`（OpDebugContinue）
- `"debug.pause"`（OpDebugPause）
- `"debug.terminate"`（OpDebugTerminate）
- `"debug.restart"`（OpDebugRestart）
- `"debug.runLast"`（OpDebugRunLast）
- `"debug.stepOver"`（OpDebugStepOver）
- `"debug.stepIn"`（OpDebugStepIn）
- `"debug.stepOut"`（OpDebugStepOut）
- `"debug.toggleUI"`（OpDebugToggleUI）

<!-- generated:manifest:end -->

## メソッド

params と result の型の項目は、`vi-imitation --docs extension-types <型の名前>`（`vi-imitation --docs extension-types BufferOpenParams`）で 1 つずつ出す。
位置（`Position`）は、0 から数えた行の番号と、行の先頭から数えた UTF-8 のバイト数の組である。

<!-- generated:methods:begin -->
#### 本体から拡張機能へ

- `$/cancel`: 通知。params は `CancelParams`。
- `buffer/change`: 通知。params は `BufferChangeParams`。
- `buffer/close`: 通知。params は `BufferCloseParams`。
- `buffer/didSave`: 通知。params は `BufferDidSaveParams`。
- `buffer/open`: 通知。params は `BufferOpenParams`。
- `buffer/willSave`: リクエスト。params は `BufferWillSaveParams`、result は `BufferWillSaveResult`。宣言ファイルの saveHook が true の稼働中の拡張機能にだけ、保存の直前に送る。
- `command/execute`: リクエスト。params は `CommandExecuteParams`、result は `CommandExecuteResult`。宣言ファイルの contributes.commands に書いたコマンドを実行する（仮）。
- `initialize`: リクエスト。params は `InitializeParams`、result は `InitializeResult`。応答が届いた時点で、拡張機能は稼働中になる。
- `input/change`: 通知。params は `InputChangeParams`。
- `motion/resolve`: リクエスト。params は `MotionResolveParams`、result は `MotionResolveResult`。
- `operator/apply`: リクエスト。params は `OperatorApplyParams`、result は `OperatorApplyResult`。
- `ping`: リクエスト。params は `PingParams`、result は `PingResult`。
- `provider/invoke`: リクエスト。params は `ProviderInvokeParams`、result は `ProviderInvokeResult`。本体の操作（Operation）を、宣言ファイルの provides で名乗った拡張機能（提供者）へ問い合わせる。本体は、利用者が操作のキー（<leader>lh など）を打つか、コマンドラインから操作の名前で呼んだ（:rename 新しい名前）ときに、カーソルのあるバッファのファイルの種類で提供者を選んで送る（選び方は Manifest.Provides の doc）。応答の扱いは command/execute と同じで、言語サーバーやデバッグアダプターの応答を待つ操作は、受け付けたことだけを ok で返し、結果を後から editor/open、popup/show、buffer/edit などで届けてよい。
- `shutdown`: 通知。params は `ShutdownParams`。本体が拡張機能を正常に止めるとき（エディタの終了、拡張機能の無効化）に送る。
- `textObject/resolve`: リクエスト。params は `TextObjectResolveParams`、result は `TextObjectResolveResult`。
- `view/closed`: 通知。params は `ViewClosedParams`。
- `view/expand`: 通知。params は `ViewExpandParams`。
- `view/key`: 通知。params は `ViewKeyParams`。
- `view/select`: 通知。params は `ViewSelectParams`。
- `view/tabChange`: 通知。params は `ViewTabChangeParams`。

#### 拡張機能から本体へ

- `buffer/create`: リクエスト。params は `BufferCreateParams`、result は `BufferCreateResult`。
- `buffer/dispose`: リクエスト。params は `BufferDisposeParams`、result は `BufferDisposeResult`。
- `buffer/edit`: リクエスト。params は `BufferEditParams`、result は `BufferEditResult`。編集を禁止したバッファへの要求は、result ではなく、code が ErrorCodeReadOnly の誤りの応答で断る。
- `buffer/resync`: 通知。params は `BufferResyncParams`。差分が欠けたと判断した拡張機能が、全文の送り直しを求める。
- `buffer/save`: リクエスト。params は `BufferSaveParams`、result は `BufferSaveResult`。
- `buffer/update`: リクエスト。params は `BufferUpdateParams`、result は `BufferUpdateResult`。
- `choice/ask`: リクエスト。params は `ChoiceAskParams`、result は `ChoiceAskResult`。利用者の応答を待つので、本体はキー入力ごとの問い合わせの期限を適用しない。
- `decoration/set`: 通知。params は `DecorationSetParams`。
- `diagnostic/list`: リクエスト。params は `DiagnosticListParams`、result は `DiagnosticListResult`。
- `diagnostic/set`: 通知。params は `DiagnosticSetParams`。
- `diff/start`: リクエスト。params は `DiffStartParams`、result は `DiffStartResult`。
- `editor/execute`: リクエスト。params は `EditorExecuteParams`、result は `EditorExecuteResult`。editor/list の keys と commands の項目を、利用者が打ったのと同じく実行させる（ファインダーで選んだキーとコマンド）。help の項目は、その語の :help を開かせる（API 0.13.0）。
- `editor/list`: リクエスト。params は `EditorListParams`、result は `EditorListResult`。
- `editor/open`: リクエスト。params は `EditorOpenParams`、result は `EditorOpenResult`。
- `editor/state`: リクエスト。params は `EditorStateParams`、result は `EditorStateResult`。
- `message/show`: 通知。params は `MessageShowParams`。
- `output/append`: 通知。params は `OutputAppendParams`。
- `pane/open`: リクエスト。params は `PaneOpenParams`、result は `PaneOpenResult`。
- `popup/open`: リクエスト。params は `PopupOpenParams`、result は `PopupOpenResult`。
- `popup/show`: 通知。params は `PopupShowParams`。
- `sign/set`: 通知。params は `SignSetParams`。
- `statusline/set`: 通知。params は `StatusLineSetParams`。宣言ファイルの contributes.statusline で足した部品の中身を送る。本体は、部品の ID ごとに最後に届いた中身を持ち、利用者が設定の statusline か tabline に並べた部品だけを描く。並べていない部品の中身も持つので、拡張機能は設定を知らずに送ってよい。宣言ファイルで足していない部品の ID と、他の拡張機能が足した部品の ID の通知は捨てる。拡張機能が異常で終わるか止まったら、その拡張機能の部品の中身をすべて消す（他の表示のデータと同じ）。中身はバッファに依らず、どの client のどのバッファのステータス行にも同じものを出す（仮。lualine の mrreview の状態と、本体のメモリの使用量のような、エディタ全体の状態を出す部品を想定した）。
- `view/close`: 通知。params は `ViewCloseParams`。
- `view/update`: 通知。params は `ViewUpdateParams`。
<!-- generated:methods:end -->
