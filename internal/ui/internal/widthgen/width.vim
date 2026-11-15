" 表示幅の表の元データを書き出す。widthgen が `vim -Nu NONE -es` で読み込む。
" 出力先は環境変数 WIDTHGEN_OUT のファイル。
" 各行は「開始 終わり（16 進） 'a' の後に置いた幅 単独の幅（10 進）」で、2 つの幅の組が変わる位置で区切る。
" 'a' の後に置いた幅は strdisplaywidth('a' .. c) - 1、単独の幅は strdisplaywidth(c)。
" U+D800〜U+DFFF（サロゲート）は -2 -2 とする。
set encoding=utf-8
let s:out = []
let s:prev = ''
let s:start = 1
for s:c in range(1, 0x10FFFF)
  if s:c >= 0xD800 && s:c <= 0xDFFF
    let s:w = '-2 -2'
  else
    let s:ch = nr2char(s:c)
    let s:w = (strdisplaywidth('a' .. s:ch) - 1) .. ' ' .. strdisplaywidth(s:ch)
  endif
  if s:w !=# s:prev
    if s:prev !=# ''
      call add(s:out, printf('%x %x %s', s:start, s:c - 1, s:prev))
    endif
    let s:prev = s:w
    let s:start = s:c
  endif
endfor
call add(s:out, printf('%x %x %s', s:start, 0x10FFFF, s:prev))
call writefile(s:out, $WIDTHGEN_OUT)
qa!
