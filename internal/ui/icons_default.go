package ui

// defaultIcons は、既定のアイコンの表である（FR-152。#295 のオーナーの決め）。データだけを持つ。
//
// ファイルの種類のアイコンと IconFile は、nvim-web-devicons（https://github.com/nvim-tree/nvim-web-devicons、
// コミット 0dc0cfb0c6858041174a53c7aa4adaf65e994b3c。MIT License）の暗い背景の表から、文字と色（color）を写した。
// 写した元は行ごとのコメントにある。icons_by_file_extension.lua と icons_by_filename.lua は lua/nvim-web-devicons/default/ の下の表、
// IconFile は lua/nvim-web-devicons.lua の default_icon である。ファイルの種類の名前から表の項目への対応は、
// nvim-web-devicons の lua/nvim-web-devicons/filetypes.lua（nvim のファイルの種類から表の項目の名前への対応）に従った。
// filetypes.lua に無い種類のうち、gomod、gosum、gowork は、その種類を決めるファイル名の項目を写した。
// terraform も filetypes.lua に無い（filetypes.lua は種類 tf を項目 tf に対応させるだけである）ので、拡張子 tf の項目を写した。
//
// ディレクトリのアイコンは、nvim-web-devicons に無いので、neo-tree.nvim（https://github.com/nvim-neo-tree/neo-tree.nvim、
// コミット 1a14083046d96e88e361d1da00761c40d45012f3。MIT License）の既定（lua/neo-tree/defaults.lua の
// default_component_configs.icon の folder_closed と folder_open）を写した。色は、lua/neo-tree/ui/highlights.lua の
// NeoTreeDirectoryIcon の、リンク先の Directory が無いときの色である。
//
// どちらのライセンスの文面も、配布物の licenses/ に入れる（Makefile の dist）。
//
// 表に入れたファイルの種類は、本体が判定できる種類（internal/editor の defaultFileTypes）の全部と、
// 設定の fileTypes で足すことの多い種類である。文字はどれも Nerd Font の私用領域のグリフなので、
// Nerd Font の入っていない端末では、設定の icons.show を false にする（config.example.jsonc）。
// 文字は、Nerd Font の無い端末でもソースを読めるよう、\U の 8 桁の書き方で書く。
var defaultIcons = Icons{
	IconDirectory:     {Text: "\U0000e5ff", Attr: Attr{Fg: RGB(0x73, 0xce, 0xf4)}}, // neo-tree の folder_closed
	IconDirectoryOpen: {Text: "\U0000e5fe", Attr: Attr{Fg: RGB(0x73, 0xce, 0xf4)}}, // neo-tree の folder_open
	IconFile:          {Text: "\U0000f0f6", Attr: Attr{Fg: RGB(0x6d, 0x80, 0x86)}}, // nvim-web-devicons の default_icon

	// 本体が判定できる種類（defaultFileTypes）。
	IconFileTypePrefix + "go":         {Text: "\U0000e627", Attr: Attr{Fg: RGB(0x00, 0xad, 0xd8)}}, // icons_by_file_extension.lua の go
	IconFileTypePrefix + "gomod":      {Text: "\U0000e627", Attr: Attr{Fg: RGB(0x00, 0xad, 0xd8)}}, // icons_by_filename.lua の go.mod
	IconFileTypePrefix + "markdown":   {Text: "\U0000e609", Attr: Attr{Fg: RGB(0xdd, 0xdd, 0xdd)}}, // icons_by_file_extension.lua の markdown
	IconFileTypePrefix + "json":       {Text: "\U0000e60b", Attr: Attr{Fg: RGB(0xcb, 0xcb, 0x41)}}, // icons_by_file_extension.lua の json
	IconFileTypePrefix + "jsonc":      {Text: "\U0000e60b", Attr: Attr{Fg: RGB(0xcb, 0xcb, 0x41)}}, // icons_by_file_extension.lua の jsonc
	IconFileTypePrefix + "python":     {Text: "\U0000e606", Attr: Attr{Fg: RGB(0xff, 0xbc, 0x03)}}, // icons_by_file_extension.lua の py
	IconFileTypePrefix + "c":          {Text: "\U0000e61e", Attr: Attr{Fg: RGB(0x59, 0x9e, 0xff)}}, // icons_by_file_extension.lua の c
	IconFileTypePrefix + "javascript": {Text: "\U0000e60c", Attr: Attr{Fg: RGB(0xcb, 0xcb, 0x41)}}, // icons_by_file_extension.lua の js
	IconFileTypePrefix + "typescript": {Text: "\U0000e628", Attr: Attr{Fg: RGB(0x51, 0x9a, 0xba)}}, // icons_by_file_extension.lua の ts
	IconFileTypePrefix + "rust":       {Text: "\U0000e68b", Attr: Attr{Fg: RGB(0xde, 0xa5, 0x84)}}, // icons_by_file_extension.lua の rs
	IconFileTypePrefix + "sh":         {Text: "\U0000e795", Attr: Attr{Fg: RGB(0x4d, 0x5a, 0x5e)}}, // icons_by_file_extension.lua の sh
	IconFileTypePrefix + "yaml":       {Text: "\U0000e8eb", Attr: Attr{Fg: RGB(0xd7, 0x00, 0x00)}}, // icons_by_file_extension.lua の yaml
	IconFileTypePrefix + "toml":       {Text: "\U0000e6b2", Attr: Attr{Fg: RGB(0x9c, 0x42, 0x21)}}, // icons_by_file_extension.lua の toml
	IconFileTypePrefix + "make":       {Text: "\U0000e779", Attr: Attr{Fg: RGB(0x6d, 0x80, 0x86)}}, // icons_by_filename.lua の makefile

	// 設定の fileTypes で足すことの多い種類（名前は nvim のファイルの種類の名前）。
	IconFileTypePrefix + "bash":            {Text: "\U0000e760", Attr: Attr{Fg: RGB(0x89, 0xe0, 0x51)}}, // icons_by_file_extension.lua の bash
	IconFileTypePrefix + "cmake":           {Text: "\U0000e794", Attr: Attr{Fg: RGB(0xdc, 0xe3, 0xeb)}}, // icons_by_file_extension.lua の cmake
	IconFileTypePrefix + "conf":            {Text: "\U0000e615", Attr: Attr{Fg: RGB(0x6d, 0x80, 0x86)}}, // icons_by_file_extension.lua の conf
	IconFileTypePrefix + "cpp":             {Text: "\U0000e61d", Attr: Attr{Fg: RGB(0x51, 0x9a, 0xba)}}, // icons_by_file_extension.lua の cpp
	IconFileTypePrefix + "cs":              {Text: "\U000f031b", Attr: Attr{Fg: RGB(0x59, 0x67, 0x06)}}, // icons_by_file_extension.lua の cs
	IconFileTypePrefix + "css":             {Text: "\U0000e6b8", Attr: Attr{Fg: RGB(0x66, 0x33, 0x99)}}, // icons_by_file_extension.lua の css
	IconFileTypePrefix + "csv":             {Text: "\U0000e64a", Attr: Attr{Fg: RGB(0x89, 0xe0, 0x51)}}, // icons_by_file_extension.lua の csv
	IconFileTypePrefix + "dart":            {Text: "\U0000e798", Attr: Attr{Fg: RGB(0x03, 0x58, 0x9c)}}, // icons_by_file_extension.lua の dart
	IconFileTypePrefix + "diff":            {Text: "\U0000e728", Attr: Attr{Fg: RGB(0x41, 0x53, 0x5b)}}, // icons_by_file_extension.lua の diff
	IconFileTypePrefix + "dockerfile":      {Text: "\U000f0868", Attr: Attr{Fg: RGB(0x45, 0x8e, 0xe6)}}, // icons_by_filename.lua の dockerfile
	IconFileTypePrefix + "dosini":          {Text: "\U0000e615", Attr: Attr{Fg: RGB(0x6d, 0x80, 0x86)}}, // icons_by_file_extension.lua の ini
	IconFileTypePrefix + "elixir":          {Text: "\U0000e62d", Attr: Attr{Fg: RGB(0xb8, 0x8c, 0xd9)}}, // icons_by_file_extension.lua の ex
	IconFileTypePrefix + "erlang":          {Text: "\U0000e7b1", Attr: Attr{Fg: RGB(0xb8, 0x39, 0x98)}}, // icons_by_file_extension.lua の erl
	IconFileTypePrefix + "fish":            {Text: "\U0000e795", Attr: Attr{Fg: RGB(0x4d, 0x5a, 0x5e)}}, // icons_by_file_extension.lua の fish
	IconFileTypePrefix + "gitcommit":       {Text: "\U0000e702", Attr: Attr{Fg: RGB(0xf5, 0x4d, 0x27)}}, // icons_by_filename.lua の commit_editmsg
	IconFileTypePrefix + "gitignore":       {Text: "\U0000e702", Attr: Attr{Fg: RGB(0xf5, 0x4d, 0x27)}}, // icons_by_filename.lua の .gitignore
	IconFileTypePrefix + "gosum":           {Text: "\U0000e627", Attr: Attr{Fg: RGB(0x00, 0xad, 0xd8)}}, // icons_by_filename.lua の go.sum
	IconFileTypePrefix + "gowork":          {Text: "\U0000e627", Attr: Attr{Fg: RGB(0x00, 0xad, 0xd8)}}, // icons_by_filename.lua の go.work
	IconFileTypePrefix + "graphql":         {Text: "\U0000f20e", Attr: Attr{Fg: RGB(0xe5, 0x35, 0xab)}}, // icons_by_file_extension.lua の graphql
	IconFileTypePrefix + "haskell":         {Text: "\U0000e61f", Attr: Attr{Fg: RGB(0xa0, 0x74, 0xc4)}}, // icons_by_file_extension.lua の hs
	IconFileTypePrefix + "html":            {Text: "\U0000e736", Attr: Attr{Fg: RGB(0xe4, 0x4d, 0x26)}}, // icons_by_file_extension.lua の html
	IconFileTypePrefix + "java":            {Text: "\U0000e738", Attr: Attr{Fg: RGB(0xcc, 0x3e, 0x44)}}, // icons_by_file_extension.lua の java
	IconFileTypePrefix + "javascriptreact": {Text: "\U0000e625", Attr: Attr{Fg: RGB(0x20, 0xc2, 0xe3)}}, // icons_by_file_extension.lua の jsx
	IconFileTypePrefix + "julia":           {Text: "\U0000e624", Attr: Attr{Fg: RGB(0xa2, 0x70, 0xba)}}, // icons_by_file_extension.lua の jl
	IconFileTypePrefix + "kotlin":          {Text: "\U0000e634", Attr: Attr{Fg: RGB(0x7f, 0x52, 0xff)}}, // icons_by_file_extension.lua の kt
	IconFileTypePrefix + "lua":             {Text: "\U0000e620", Attr: Attr{Fg: RGB(0x51, 0xa0, 0xcf)}}, // icons_by_file_extension.lua の lua
	IconFileTypePrefix + "nix":             {Text: "\U0000f313", Attr: Attr{Fg: RGB(0x7e, 0xba, 0xe4)}}, // icons_by_file_extension.lua の nix
	IconFileTypePrefix + "ocaml":           {Text: "\U0000e67a", Attr: Attr{Fg: RGB(0xe3, 0x79, 0x33)}}, // icons_by_file_extension.lua の ml
	IconFileTypePrefix + "perl":            {Text: "\U0000e769", Attr: Attr{Fg: RGB(0x51, 0x9a, 0xba)}}, // icons_by_file_extension.lua の pl
	IconFileTypePrefix + "php":             {Text: "\U0000e608", Attr: Attr{Fg: RGB(0xa0, 0x74, 0xc4)}}, // icons_by_file_extension.lua の php
	IconFileTypePrefix + "r":               {Text: "\U000f07d4", Attr: Attr{Fg: RGB(0x22, 0x66, 0xba)}}, // icons_by_file_extension.lua の r
	IconFileTypePrefix + "ruby":            {Text: "\U0000e791", Attr: Attr{Fg: RGB(0x70, 0x15, 0x16)}}, // icons_by_file_extension.lua の rb
	IconFileTypePrefix + "scala":           {Text: "\U0000e737", Attr: Attr{Fg: RGB(0xcc, 0x3e, 0x44)}}, // icons_by_file_extension.lua の scala
	IconFileTypePrefix + "scss":            {Text: "\U0000e603", Attr: Attr{Fg: RGB(0xf5, 0x53, 0x85)}}, // icons_by_file_extension.lua の scss
	IconFileTypePrefix + "sql":             {Text: "\U0000e706", Attr: Attr{Fg: RGB(0xda, 0xd8, 0xd8)}}, // icons_by_file_extension.lua の sql
	IconFileTypePrefix + "svelte":          {Text: "\U0000e697", Attr: Attr{Fg: RGB(0xff, 0x3e, 0x00)}}, // icons_by_file_extension.lua の svelte
	IconFileTypePrefix + "swift":           {Text: "\U0000e755", Attr: Attr{Fg: RGB(0xe3, 0x79, 0x33)}}, // icons_by_file_extension.lua の swift
	IconFileTypePrefix + "terraform":       {Text: "\U0000e69a", Attr: Attr{Fg: RGB(0x5f, 0x43, 0xe9)}}, // icons_by_file_extension.lua の tf
	IconFileTypePrefix + "tex":             {Text: "\U0000e69b", Attr: Attr{Fg: RGB(0x3d, 0x61, 0x17)}}, // icons_by_file_extension.lua の tex
	IconFileTypePrefix + "text":            {Text: "\U000f0219", Attr: Attr{Fg: RGB(0x89, 0xe0, 0x51)}}, // icons_by_file_extension.lua の txt
	IconFileTypePrefix + "typescriptreact": {Text: "\U0000e7ba", Attr: Attr{Fg: RGB(0x13, 0x54, 0xbf)}}, // icons_by_file_extension.lua の tsx
	IconFileTypePrefix + "vim":             {Text: "\U0000e62b", Attr: Attr{Fg: RGB(0x01, 0x98, 0x33)}}, // icons_by_file_extension.lua の vim
	IconFileTypePrefix + "vue":             {Text: "\U0000e6a0", Attr: Attr{Fg: RGB(0x8d, 0xc1, 0x49)}}, // icons_by_file_extension.lua の vue
	IconFileTypePrefix + "xml":             {Text: "\U000f05c0", Attr: Attr{Fg: RGB(0xe3, 0x79, 0x33)}}, // icons_by_file_extension.lua の xml
	IconFileTypePrefix + "zig":             {Text: "\U0000e6a9", Attr: Attr{Fg: RGB(0xf6, 0x9a, 0x1b)}}, // icons_by_file_extension.lua の zig
	IconFileTypePrefix + "zsh":             {Text: "\U0000e795", Attr: Attr{Fg: RGB(0x89, 0xe0, 0x51)}}, // icons_by_file_extension.lua の zsh
}
