// Package sample は、reqtrace のテストが走査する入力である。
package sample

// Move は、行コメントの注釈の例である。
//
// req: FR-01, FR-02
func Move() {}

/*
Delete は、ブロックコメントの注釈の例である。

req: FR-03
*/
func Delete() {}

// Text は、文字列の中の "req:" が注釈として扱われないことを確かめるための値である。
const Text = "req: FR-99"

// Unknown は、要件定義に無い ID の例である。
//
// req: FR-77
func Unknown() {}
