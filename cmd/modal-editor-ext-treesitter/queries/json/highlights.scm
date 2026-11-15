; 出典: https://github.com/tree-sitter/tree-sitter-json/blob/ee35a6ebefcef0c5c416c0d1ccec7370cfca5a24/queries/highlights.scm
; tree-sitter-json v0.24.8 の公式のハイライトのクエリを改変せずに写したもの。ライセンスは同じディレクトリの LICENSE（MIT）。

(pair
  key: (_) @string.special.key)

(string) @string

(number) @number

[
  (null)
  (true)
  (false)
] @constant.builtin

(escape_sequence) @escape

(comment) @comment
