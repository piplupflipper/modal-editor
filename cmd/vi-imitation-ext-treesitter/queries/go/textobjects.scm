; 構文に沿ったテキストオブジェクトのクエリ（基本設計 6.1）。このリポジトリで書いたもので、公式の文法には同名のファイルが無い。
;
; キャプチャ名は <対象>.<部分> である（textobject.go の objectKinds）。
;   @X.outer: 全体（aX）の範囲になるノード
;   @X.inner: 内側（iX）のもとになるノード。括弧で囲まれていれば括弧の内側を、そうでなければノードそのものを内側とする
; @X.outer の無いパターン（parameter）は、@X.inner と、前か後ろの区切りの "," までを全体とする。
; 全体が同じ範囲のパターンが複数当たれば、先に書いたパターンを採る（textobject.go の candidates）。

; 関数（af、if）。本体の無い宣言は全体だけを持つ。
(function_declaration
  body: (block) @function.inner) @function.outer
(function_declaration) @function.outer
(method_declaration
  body: (block) @function.inner) @function.outer
(method_declaration) @function.outer
(func_literal
  body: (block) @function.inner) @function.outer

; 型（ac、ic）。Go にクラスは無いので、型の宣言を当てる。
; 宣言が 1 つの型だけを持てば type から、括弧でまとめた宣言なら型の名前からを全体とする。
; 構造体とインターフェースの内側は、波括弧の内側である。その他の型（type X int）の内側は、型の式である。
(type_declaration
  .
  (type_spec
    type: (struct_type (field_declaration_list) @class.inner))
  .) @class.outer
(type_spec
  type: (struct_type (field_declaration_list) @class.inner)) @class.outer
(type_declaration
  .
  (type_spec
    type: (interface_type) @class.inner)
  .) @class.outer
(type_spec
  type: (interface_type) @class.inner) @class.outer
(type_declaration
  .
  (type_spec
    type: (_) @class.inner)
  .) @class.outer
(type_spec
  type: (_) @class.inner) @class.outer
(type_declaration
  .
  (type_alias
    type: (_) @class.inner)
  .) @class.outer
(type_alias
  type: (_) @class.inner) @class.outer

; 引数（aa、ia）。関数の宣言の引数と受け手、呼び出しの引数、型引数
(parameter_list
  (parameter_declaration) @parameter.inner)
(parameter_list
  (variadic_parameter_declaration) @parameter.inner)
(argument_list
  (_) @parameter.inner)
(type_parameter_list
  (type_parameter_declaration) @parameter.inner)
(type_arguments
  (_) @parameter.inner)
