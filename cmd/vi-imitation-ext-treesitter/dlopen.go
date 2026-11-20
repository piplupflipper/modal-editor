package main

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>

typedef const void *(*vi_language_fn)(void);

// vi_load_language は、path の共有ライブラリを読み、symbol の関数を呼んで TSLanguage へのポインターを返す。
// 失敗したら NULL を返し、理由を err に書く。
// dlerror はスレッドごとの値なので、dlopen と dlsym と dlerror を 1 回の C の呼び出しの中で続けて呼ぶ。
// 読んだライブラリは閉じない（返したポインターは、そのライブラリの中の静的なデータを指す）。
static const void *vi_load_language(const char *path, const char *symbol, char *err, size_t errlen) {
	dlerror();
	void *h = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (h == NULL) {
		const char *e = dlerror();
		snprintf(err, errlen, "%s", e != NULL ? e : "dlopen failed");
		return NULL;
	}
	dlerror();
	void *fn = dlsym(h, symbol);
	const char *e = dlerror();
	if (e != NULL || fn == NULL) {
		snprintf(err, errlen, "%s", e != NULL ? e : "symbol is NULL");
		dlclose(h);
		return NULL;
	}
	const void *lang = ((vi_language_fn)fn)();
	if (lang == NULL) {
		snprintf(err, errlen, "%s returned NULL", symbol);
		return NULL;
	}
	return lang;
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// dlErrLen は、vi_load_language が誤りの文言を書く領域の大きさである。
const dlErrLen = 1024

// loadGrammar は、path の共有ライブラリ（利用者の手元でビルドしたパーサ。FR-142）を dlopen で読み、
// symbol（tree_sitter_<文法の名前>）の関数が返す TSLanguage へのポインターを返す。
//
// このために、tree-sitter の拡張機能は glibc に動的にリンクする（要件定義の NFR-01。musl を静的にリンクした実行ファイルは dlopen を使えない）。
// 読んだライブラリは、拡張機能が終わるまで閉じない。
//
// req: FR-142
func loadGrammar(path, symbol string) (unsafe.Pointer, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	csym := C.CString(symbol)
	defer C.free(unsafe.Pointer(csym))
	cerr := (*C.char)(C.calloc(dlErrLen, 1))
	defer C.free(unsafe.Pointer(cerr))
	lang := C.vi_load_language(cpath, csym, cerr, dlErrLen)
	if lang == nil {
		return nil, errors.New(C.GoString(cerr))
	}
	return lang, nil
}
